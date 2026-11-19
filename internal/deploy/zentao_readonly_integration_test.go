package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Explicit opt-in: inspect response field names and enum dictionaries only.
// The runtime DB is read-only; native calls are authentication and GET forms.
func TestZentaoResolutionDictionaryReadOnly(t *testing.T) {
	dir, name, bugID := os.Getenv("DEVOPS_ZENTAO_TEST_DATA"), os.Getenv("DEVOPS_ZENTAO_TEST_CONNECTION"), os.Getenv("DEVOPS_ZENTAO_TEST_BUG")
	if dir == "" || name == "" || bugID == "" {
		t.Skip("requires explicit real-service data/connection/Bug opt in")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "deploy.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(dir, ".credential-key"))
	if err != nil {
		t.Fatal("credential key unavailable")
	}
	s := &Store{db: &deployDB{DB: db, dialect: "sqlite"}, secretKey: key}
	c := s.GetConnection(name)
	if c == nil {
		t.Fatal("connection not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	z, err := newZentaoLegacy(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"view", "resolve"} {
		var payload map[string]json.RawMessage
		if err := z.call(ctx, "bug", method, url.Values{"bugID": {bugID}}, nil, &payload); err != nil {
			t.Logf("GET %s: %v", method, err)
			continue
		}
		keys := []string{}
		for k := range payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Logf("GET %s response fields: %v", method, keys)
		for _, field := range []string{"resolutions", "resolutionList", "lang"} {
			if value, ok := payload[field]; ok {
				if field == "lang" {
					var lang map[string]json.RawMessage
					json.Unmarshal(value, &lang)
					var bug map[string]json.RawMessage
					json.Unmarshal(lang["bug"], &bug)
					value = bug["resolutionList"]
				}
				var labels map[string]string
				if json.Unmarshal(value, &labels) == nil {
					t.Logf("GET %s %s dictionary: %v", method, field, labels)
				}
			}
		}
	}
	remote, err := z.researchRead(ctx, bugID, "view")
	if err != nil {
		t.Fatal(err)
	}
	z.enrichResearchResolution(ctx, c, remote)
	code := researchValue(remote.Bug, "resolution")
	if code != "" && remote.Resolutions[code] == "" {
		t.Fatal("native resolution still has no label", code)
	}
	t.Logf("actual source resolution dictionary: %v; source_Bug_writes=0 runtime_DB_writes=0", remote.Resolutions)
}

func TestZentaoBugListModelReadOnly(t *testing.T) {
	dir, name := os.Getenv("DEVOPS_ZENTAO_TEST_DATA"), os.Getenv("DEVOPS_ZENTAO_TEST_CONNECTION")
	if dir == "" || name == "" {
		t.Skip("requires explicit real-service opt in")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "deploy.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(dir, ".credential-key"))
	if err != nil {
		t.Fatal("credential key unavailable")
	}
	s := &Store{db: &deployDB{DB: db, dialect: "sqlite"}, secretKey: key}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	z, c, _, err := s.liveReadClient(ctx, name, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	var user pendingSourceUser
	if err = z.call(ctx, "user", "login", nil, nil, &user); err != nil {
		t.Fatal(err)
	}
	if user.User.Account != c.Username || user.User.View == nil || user.User.Admin == nil {
		t.Fatal("source did not provide authenticated permission scope")
	}
	products, err := sourceIDSet(user.User.View.Products)
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := sourceIDSet(user.User.View.Sprints)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		start := time.Now()
		var raw json.RawMessage
		if err = z.call(ctx, "api", "getModel", url.Values{"moduleName": {"bug"}, "methodName": {"getByList"}, "params": {"bugIDList=26087,26203,fields=" + bugListFields}}, nil, &raw); err != nil {
			t.Fatal(err)
		}
		rows, err := decodeLiveBugs(raw)
		if err != nil || len(rows) != 2 {
			t.Fatal("batch response", err, len(rows))
		}
		for _, row := range rows {
			if researchValue(row, "id") != "26087" && researchValue(row, "id") != "26203" {
				t.Fatal("unexpected ID returned")
			}
			if _, ok := row["steps"]; ok {
				t.Fatal("model returned unrequested long body")
			}
		}
		t.Logf("read-only batch: requested=2 returned=%d duration=%s source_products=%d source_sprints=%d; imported=0 lifecycle_writes=0", len(rows), time.Since(start).Round(time.Millisecond), len(products), len(sprints))
	}
	// Exercise a nonempty queue without registering or changing anything in the
	// runtime database. Only this private, disposable fixture holds references.
	probe, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err = probe.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	if err = probe.UpsertEnvironment(Environment{Name: "prod", DisplayName: "Probe production", Purpose: "production"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"26087", "26203"} {
		item, err := probe.AttachResearchBug(ctx, "isolated-probe", name, id)
		if err != nil {
			t.Fatal(err)
		}
		yes := true
		if err = probe.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &yes}, "isolated-probe"); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		start := time.Now()
		out, err := probe.ResearchLivePage(ctx, "isolated-probe", researchLiveFilter{Connection: name, Delivery: "pending_release", WorkItemFilter: WorkItemFilter{PageSize: 20}})
		if err != nil || len(out.Items) != 2 || out.QueryMode != "candidate_batch" || !out.TotalExact {
			t.Fatal("isolated nonempty pending query failed", err)
		}
		t.Logf("isolated nonempty pending query: rows=%d duration=%s mode=%s; runtime_registration=0 lifecycle_writes=0", len(out.Items), time.Since(start).Round(time.Millisecond), out.QueryMode)
	}
}

// Explicit opt-in smoke test: decrypt credentials in memory from a read-only
// SQLite store and query ZenTao only. Never imports Bugs/users or logs records.
func TestZentaoReadOnlyDateScope(t *testing.T) {
	dir, name := os.Getenv("DEVOPS_ZENTAO_TEST_DATA"), os.Getenv("DEVOPS_ZENTAO_TEST_CONNECTION")
	if dir == "" || name == "" {
		t.Skip("requires explicit real-service opt in")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "deploy.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(dir, ".credential-key"))
	if err != nil {
		t.Fatal("cannot read credential key")
	}
	st := &Store{db: &deployDB{DB: db, dialect: "sqlite"}, secretKey: key}
	c := st.GetConnection(name)
	if c == nil {
		t.Fatal("connection not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	z, err := newZentaoLegacy(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := zentaoProductIDs(c)
	if err != nil {
		t.Fatal(err)
	}
	baseline := os.Getenv("DEVOPS_ZENTAO_TEST_BASELINE")
	if baseline == "" {
		t.Fatal("explicit baseline is required")
	}
	if _, err = time.Parse("2006-01-02", baseline); err != nil {
		t.Fatal("invalid baseline")
	}
	rows, err := z.filteredBugs(ctx, ids, baseline, "closed")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read-only scope: products=%d closed_matching=%d baseline=%s; imported=0", len(ids), len(rows), baseline)
}

func TestZentaoLiveQueryReadOnly(t *testing.T) {
	dir, name := os.Getenv("DEVOPS_ZENTAO_TEST_DATA"), os.Getenv("DEVOPS_ZENTAO_TEST_CONNECTION")
	if dir == "" || name == "" {
		t.Skip("requires explicit real-service opt in")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "deploy.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(dir, ".credential-key"))
	if err != nil {
		t.Fatal("credential key unavailable")
	}
	s := &Store{db: &deployDB{DB: db, dialect: "sqlite"}, secretKey: key}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	seen := map[string]bool{}
	cursor := ""
	for page := 1; page <= 2; page++ {
		out, err := s.ResearchLivePage(ctx, "read-only-query-probe", researchLiveFilter{Connection: name, Cursor: cursor, WorkItemFilter: WorkItemFilter{Page: page, PageSize: 20, Order: "updated_desc"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range out.Items {
			if seen[w.ExternalID] {
				t.Fatal("source pagination repeats")
			}
			seen[w.ExternalID] = true
			if w.ReadAt == 0 {
				t.Fatal("source fields missing")
			}
		}
		t.Logf("live page=%d count=%d total=%d exact=%t products=%d people=%d; local_imports=0", out.Page, len(out.Items), out.Total, out.TotalExact, len(out.Products), len(out.People))
		cursor = out.NextCursor
	}
	out, err := s.ResearchLivePage(ctx, "read-only-query-probe", researchLiveFilter{Connection: name, WorkItemFilter: WorkItemFilter{Status: "closed", PageSize: 20, Order: "priority_asc"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range out.Items {
		if w.ExternalStatus != "closed" {
			t.Fatal("closed filter ignored")
		}
	}
	t.Logf("closed status + priority sort: count=%d total=%d; Bug lifecycle writes=0", len(out.Items), out.Total)
	if len(out.Items) > 0 {
		id := out.Items[0].ExternalID
		filtered, err := s.ResearchLivePage(ctx, "read-only-query-probe", researchLiveFilter{Connection: name, Product: out.Items[0].ProductID, WorkItemFilter: WorkItemFilter{Query: id, Status: "closed", PageSize: 20}})
		if err != nil {
			t.Fatal(err)
		}
		if len(filtered.Items) != 1 || filtered.Items[0].ExternalID != id {
			t.Fatal("ID + closed filter ignored")
		}
		t.Log("ID + status combination passed; credentials and Bug content not logged")
	}
}
