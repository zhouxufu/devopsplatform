package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type mockNacos struct {
	mu      sync.Mutex
	content string
	posts   int
	race    bool
	fail    bool
}

func (n *mockNacos) snapshot() (string, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.content, n.posts
}
func (n *mockNacos) change(s string) { n.mu.Lock(); defer n.mu.Unlock(); n.content = s }
func nacosFixture(t *testing.T) (*Store, *ReleaseManager, *mockNacos, *Change) {
	t.Helper()
	st, m, _ := deliveryFixture(t)
	n := &mockNacos{content: "# QA original\nfeature:\n  enabled: False # preserve comment\nuntouched: backup-only-secret\n"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "accessToken") {
			t.Error("token exposed in query")
		}
		if r.URL.Path == "/nacos/v1/auth/login" {
			_ = r.ParseForm()
			if r.Form.Get("username") != "qa" || r.Form.Get("password") != "qa-password" {
				t.Error("invalid login")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"accessToken":"qa-token"}`)
			return
		}
		if r.URL.Path != "/nacos/v1/cs/configs" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer qa-token" {
			http.Error(w, "forbidden", 403)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("dataId") != "orders.yaml" || r.Form.Get("group") != "DEFAULT_GROUP" || r.Form.Get("tenant") != "qa-namespace" {
			t.Error("invalid config destination")
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		if r.Method == http.MethodGet {
			fmt.Fprint(w, n.content)
			return
		}
		n.posts++
		if n.race {
			n.content += "console-edit: true\n"
			n.race = false
		}
		if n.fail {
			http.Error(w, "upstream-private-secret", 500)
			return
		}
		if r.Header.Get("casMd5") != nacosMD5(n.content) {
			http.Error(w, "conflict", 409)
			return
		}
		n.content = r.Form.Get("content")
		fmt.Fprint(w, "true")
	}))
	t.Cleanup(server.Close)
	c := ResourceConnection{Name: "qa-nacos", Kind: "nacos", Endpoint: server.URL + "/nacos", Username: "qa", Password: "qa-password", Config: map[string]string{"nacos_version": "2.4", "nacos_auth": "password", "namespace": "qa-namespace", "test_data_id": "orders.yaml"}}
	if e := st.UpsertConnection(c); e != nil {
		t.Fatal(e)
	}
	if e := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "config", Kind: "nacos"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "config", Environment: "test", Connection: c.Name}, {Alias: "config", Environment: "prod", Connection: c.Name}}}); e != nil {
		t.Fatal(e)
	}
	change := &Change{Kind: "nacos", Title: "Enable feature", Service: "orders", Owner: "alice", Nacos: &NacosChange{ResourceAlias: "config", DataID: "orders.yaml", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}, {Key: "added.value", Action: "set", Value: "42"}}}}
	if e := st.CreateChange(change); e != nil {
		t.Fatal(e)
	}
	if _, e := st.SetChangeReady(change.ID, true); e != nil {
		t.Fatal(e)
	}
	return st, m, n, change
}
func nacosRun(t *testing.T, st *Store, m *ReleaseManager, c *Change, env string) *DeliveryRun {
	t.Helper()
	tpl := &DeliveryTemplate{Name: "Nacos QA", Mode: "fixed", Owner: "alice", Nodes: []DeliveryNode{{ID: "config", Kind: "nacos", Name: "Update configuration", ChangeIDs: []int64{c.ID}}}}
	if e := st.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	run, e := st.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, Environment: env, Reason: "QA"}, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	return run
}
func TestNacosReviewCASRecoveryAndEncryption(t *testing.T) {
	st, m, n, c := nacosFixture(t)
	before, _ := n.snapshot()
	run := nacosRun(t, st, m, c, "prod")
	if _, posts := n.snapshot(); posts != 0 {
		t.Fatal("preview published")
	}
	data, _ := json.Marshal(run)
	if strings.Contains(string(data), "qa-password") || strings.Contains(string(data), "backup-only-secret") || strings.Contains(string(data), "enc:v1:") {
		t.Fatal("public run exposes credentials or backups")
	}
	var stored string
	_ = st.db.QueryRow(`SELECT plan FROM delivery_runs WHERE id=?`, run.ID).Scan(&stored)
	if strings.Contains(stored, "backup-only-secret") || !strings.Contains(stored, "enc:v1:") {
		t.Fatal("backup not encrypted")
	}
	saved := st.DeliveryRun(run.ID)
	backup, e := st.decryptSecret(saved.Plan.NacosPlans[0].Configs[0].BeforeEncrypted)
	if e != nil || backup != before {
		t.Fatal("backup not persisted")
	}
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("execution before review")
	}
	for _, actor := range []string{"alice", "outsider"} {
		if _, e = st.ReviewDeliveryRun(run.ID, actor, run.Digest, true); e == nil {
			t.Fatal("invalid reviewer", actor)
		}
	}
	if _, e = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); e != nil {
		t.Fatal(e)
	}
	got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Steps[0].Status != "success" {
		t.Fatalf("execute: %+v %v", got, e)
	}
	got, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Status != "success" {
		t.Fatal("complete", e)
	}
	after, posts := n.snapshot()
	if posts != 1 || !strings.Contains(after, "enabled: true") || !strings.Contains(after, "preserve comment") {
		t.Fatal("publish or comments", after, posts)
	}
	if _, e = st.prepareNacosNode(context.Background(), "again", []int64{c.ID}, "prod"); e == nil {
		t.Fatal("duplicate allowed")
	}
	mux := http.NewServeMux()
	registerNacosRoutes(mux, st)
	request := httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/runs/%d/nacos-restore-draft", run.ID), strings.NewReader(fmt.Sprintf(`{"change_id":%d}`, c.ID)))
	request.Header.Set("X-User", "outsider")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("outsider restored")
	}
	request = httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/runs/%d/nacos-restore-draft", run.ID), strings.NewReader(fmt.Sprintf(`{"change_id":%d}`, c.ID)))
	request.Header.Set("X-User", "alice")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var restore Change
	if e = json.Unmarshal(response.Body.Bytes(), &restore); e != nil {
		t.Fatal(e)
	}
	if restore.Status != ChangeDraft || restore.Nacos.Restore == nil {
		t.Fatal("not a recovery draft")
	}
	if _, p := n.snapshot(); p != 1 {
		t.Fatal("recovery draft changed remote")
	}
	if _, e = st.SetChangeReady(restore.ID, true); e != nil {
		t.Fatal(e)
	}
	recovery := nacosRun(t, st, m, &restore, "prod")
	if got := recovery.Plan.NacosPlans[0].Configs[0].Diff[0].After; got == nil || *got != "False" {
		t.Fatal("recovery diff does not match the typed backup", got)
	}
	if recovery.Status != "pending_approval" {
		t.Fatal("recovery bypassed approval")
	}
	if _, e = st.ReviewDeliveryRun(recovery.ID, "reviewer", recovery.Digest, true); e != nil {
		t.Fatal(e)
	}
	got, e = st.AdvanceDeliveryRun(recovery.ID, "alice", false, m)
	if e != nil || got.Steps[0].Status != "success" {
		t.Fatalf("recovery: %+v %v", got, e)
	}
	restored, p := n.snapshot()
	if restored != before || p != 2 {
		t.Fatal("full document not restored exactly", restored, p)
	}
}
func TestNacosDriftCASRaceAndUncertainNoRetry(t *testing.T) {
	for _, scenario := range []string{"baseline", "identity", "cas-race", "upstream", "duplicate", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			st, m, n, c := nacosFixture(t)
			run := nacosRun(t, st, m, c, "test")
			switch scenario {
			case "baseline":
				n.change("feature:\n  enabled: true\n")
			case "identity":
				connection := st.GetConnection("qa-nacos")
				connection.Config["namespace"] = "different"
				if e := st.UpsertConnection(*connection); e != nil {
					t.Fatal(e)
				}
			case "cas-race":
				n.race = true
			case "upstream":
				n.fail = true
			case "duplicate":
				other := nacosRun(t, st, m, c, "test")
				if _, e := st.AdvanceDeliveryRun(other.ID, "alice", false, m); e != nil {
					t.Fatal(e)
				}
			case "interrupted":
				run.Steps[0].Status = "executing"
				run.Status = "running"
				if e := st.saveDeliveryRun(run); e != nil {
					t.Fatal(e)
				}
			}
			got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if scenario == "interrupted" {
				if e == nil {
					t.Fatal("interrupted execution retried")
				}
			} else if e != nil || got.Status != "failed" {
				t.Fatalf("did not fail closed: %+v %v", got, e)
			}
			_, posts := n.snapshot()
			expected := 0
			if scenario == "cas-race" || scenario == "upstream" || scenario == "duplicate" {
				expected = 1
			}
			if posts != expected {
				t.Fatal("unexpected publish count", posts)
			}
			if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
				t.Fatal("failed or interrupted run retried")
			}
			_, again := n.snapshot()
			if again != posts {
				t.Fatal("automatic retry")
			}
			if scenario == "cas-race" || scenario == "upstream" {
				var status string
				_ = st.db.QueryRow(`SELECT status FROM nacos_execution_ledger WHERE change_id=?`, c.ID).Scan(&status)
				if status != "uncertain" {
					t.Fatal("uncertain result not persisted")
				}
				if strings.Contains(got.Steps[0].Detail, "upstream-private-secret") {
					t.Fatal("upstream body exposed")
				}
			}
		})
	}
}
func TestNacosPreviewRouteReadOnlyAndVersionScope(t *testing.T) {
	st, m, n, c := nacosFixture(t)
	mux := http.NewServeMux()
	registerNacosRoutes(mux, st)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("POST", fmt.Sprintf("/api/changes/%d/nacos-preview", c.ID), strings.NewReader(`{"environment":"test"}`)))
	if response.Code != 200 || strings.Contains(response.Body.String(), "enc:v1:") || strings.Contains(response.Body.String(), "backup-only-secret") {
		t.Fatal(response.Body.String())
	}
	if _, p := n.snapshot(); p != 0 {
		t.Fatal("preview wrote")
	}
	v := &ReleaseVersion{Version: "config-qa", Title: "config", Owner: "alice"}
	if e := st.CreateReleaseVersion(v); e != nil {
		t.Fatal(e)
	}
	if e := st.SetReleaseVersionChanges(v.ID, []int64{c.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := st.SetReleaseVersionReady(v.ID); e != nil {
		t.Fatal(e)
	}
	tpl := &DeliveryTemplate{Name: "Version config", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "nacos", Kind: "nacos", Name: "config"}}}
	if e := st.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	p, _, e := st.ResolveDelivery(DeliveryRequest{TemplateID: tpl.ID, VersionID: v.ID, Environment: "test"}, m)
	if e != nil || len(p.Blockers) > 0 || len(p.NacosPlans) != 1 {
		t.Fatalf("version config scope: %+v %v", p, e)
	}
	tpl.Nodes = append(tpl.Nodes, DeliveryNode{ID: "duplicate", Kind: "nacos", Name: "duplicate"})
	if e := st.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	p, _, e = st.ResolveDelivery(DeliveryRequest{TemplateID: tpl.ID, VersionID: v.ID, Environment: "test"}, m)
	if e != nil || len(p.Blockers) == 0 {
		t.Fatal("duplicate nodes allowed")
	}
}
func TestNacosScalarPatchAndRejectUnsafeDocuments(t *testing.T) {
	entries := []NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}, {Key: "string", Action: "set", Value: `"123"`}, {Key: "old", Action: "delete"}}
	after, diffs, e := patchNacosContent(`{"feature":{"enabled":false},"large":9007199254740993,"old":null}`, &NacosChange{Format: "json", Entries: entries})
	if e != nil || len(diffs) != 3 || !strings.Contains(after, "9007199254740993") || !strings.Contains(after, `"123"`) {
		t.Fatal(after, diffs, e)
	}
	for _, doc := range []string{"a: 1\na: 2\n", "a: &x 1\nb: *x\n", "a: !custom 1\n", "a: 1\n---\nb: 2\n", "a: [1,2]\n"} {
		if _, _, e = patchNacosContent(doc, &NacosChange{Format: "yaml", Entries: []NacosEntry{{Key: "a", Action: "set", Value: "x"}}}); e == nil {
			t.Fatal("unsafe document accepted", doc)
		}
	}
	if _, _, e = patchNacosContent("a: 1\n", &NacosChange{Format: "yaml", Entries: []NacosEntry{{Key: "a", Action: "set", Value: `{"object":1}`}}}); e == nil {
		t.Fatal("object overwrite accepted")
	}
	after, _, e = patchNacosContent("# comment\nfeature.enabled=false\nuntouched=secret\n", &NacosChange{Format: "properties", Entries: []NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}}})
	if e != nil || after != "# comment\nfeature.enabled=true\nuntouched=secret\n" {
		t.Fatal(after, e)
	}
	for _, doc := range []string{"a=1\na=2", "a=hello\\nworld", "a hello"} {
		if _, _, e = patchNacosContent(doc, &NacosChange{Format: "properties", Entries: []NacosEntry{{Key: "a", Action: "set", Value: "x"}}}); e == nil {
			t.Fatal("unsafe properties", doc)
		}
	}
}
func TestNacosRecoveryBlocksNewerConfigurationAndChangedEnvironment(t *testing.T) {
	st, m, n, c := nacosFixture(t)
	run := nacosRun(t, st, m, c, "test")
	if got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil || got.Steps[0].Status != "success" {
		t.Fatal(got, e)
	}
	item := run.Plan.NacosPlans[0].Configs[0]
	recovery := &Change{Kind: "nacos", Title: "Restore QA", Service: "orders", Owner: "alice", Nacos: &NacosChange{ResourceAlias: item.Alias, DataID: item.DataID, Group: item.Group, Format: item.Format, Entries: nacosInverseEntries(item.Diff), Restore: &NacosRestore{RunID: run.ID, ChangeID: c.ID}}}
	if e := st.CreateChange(recovery); e != nil {
		t.Fatal(e)
	}
	if _, e := st.prepareNacosNode(context.Background(), "recovery", []int64{recovery.ID}, "prod"); e == nil {
		t.Fatal("recovered in different environment")
	}
	current, _ := n.snapshot()
	n.change(current + "console: edit\n")
	if _, e := st.prepareNacosNode(context.Background(), "recovery", []int64{recovery.ID}, "test"); e == nil {
		t.Fatal("newer document overwritten")
	}
	if _, posts := n.snapshot(); posts != 1 {
		t.Fatal("recovery preview wrote remote")
	}
}

func TestNacosCredentialModeChangeDoesNotReusePassword(t *testing.T) {
	st, _, _, _ := nacosFixture(t)
	c := *st.GetConnection("qa-nacos")
	c.Config["nacos_auth"] = "token"
	c.Password = ""
	if e := st.UpsertConnection(c); e == nil {
		t.Fatal("password reused as token")
	}
	c.Password = "qa-token"
	if e := st.UpsertConnection(c); e != nil {
		t.Fatal(e)
	}
	if e := testIntegrationConnection(context.Background(), st.GetConnection(c.Name)); e != nil {
		t.Fatal(e)
	}
}

func TestNacosClientVersionAuthenticationAndRedirect(t *testing.T) {
	for _, v := range []string{"1.4", "3.0", "mse"} {
		c := ResourceConnection{Kind: "nacos", Endpoint: "http://127.0.0.1/nacos", Config: map[string]string{"nacos_version": v}}
		if validateNacosConnection(&c) == nil {
			t.Fatal("unsupported version accepted", v)
		}
	}
	externalCalls := 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalCalls++; fmt.Fprint(w, "true") }))
	defer external.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, external.URL, 302) }))
	defer redirect.Close()
	c := ResourceConnection{Kind: "nacos", Endpoint: redirect.URL, Password: "private-token", Config: map[string]string{"nacos_auth": "token"}}
	client, e := newNacosClient(context.Background(), &c)
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.read(context.Background(), "config", "group")
	if e == nil || externalCalls != 0 || strings.Contains(e.Error(), "private-token") {
		t.Fatal("redirect followed or error leaked", e)
	}
}
