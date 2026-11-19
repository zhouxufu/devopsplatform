package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func resourceGroupFixture(t *testing.T) (*Store, *ReleaseManager, *ResourceGroup) {
	t.Helper()
	st, m, _ := deliveryFixture(t)
	for _, c := range []ResourceConnection{
		{Name: "db-dev", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"tls": "false"}},
		{Name: "db-prod", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"tls": "false"}},
		{Name: "db-second", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"tls": "false"}},
	} {
		if err := st.UpsertConnection(c); err != nil {
			t.Fatal(err)
		}
	}
	g := &ResourceGroup{Name: "business", Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}, {Alias: "extra", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: "db-dev", Config: map[string]string{"database": "dev_db"}}, {Alias: "db", Environment: "prod", Connection: "db-prod", Config: map[string]string{"database": "prod_db"}}, {Alias: "extra", Environment: "test", Connection: "db-second", Config: map[string]string{"database": "second_db"}}}}
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{ResourceGroup: g.Name, GroupRevision: g.Revision}); err != nil {
		t.Fatal(err)
	}
	return st, m, g
}
func TestResourceGroupReuseEnvironmentResolutionAndRevision(t *testing.T) {
	st, _, g := resourceGroupFixture(t)
	if err := st.UpsertService(Service{Name: "billing", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("billing", ApplicationResources{ResourceGroup: g.Name}); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"orders", "billing"} {
		for env, expected := range map[string]string{"test": "db-dev", "prod": "db-prod"} {
			c, err := st.sqlBinding(app, env, "db")
			if err != nil || c.Name != expected {
				t.Fatal("wrong group target", app, env, c, err)
			}
		}
	}
	if err := st.DeleteResourceGroup(g.Name); err == nil {
		t.Fatal("referenced group deleted")
	}
	if err := st.DeleteConnection("db-dev"); err == nil {
		t.Fatal("group connection deleted")
	}
	st.UpsertEnvironment(Environment{Name: "unused", DisplayName: "Unused"})
	g.Bindings = append(g.Bindings, ApplicationEnvironmentResource{Alias: "db", Environment: "unused", Connection: "db-dev", Config: map[string]string{"database": "dev_db"}})
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteEnvironment("unused"); err == nil {
		t.Fatal("group environment deleted")
	}
	stale := st.ResourceGroup(g.Name)
	g.Description = "new revision"
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResourceGroup(stale); err == nil {
		t.Fatal("stale group overwrote current")
	}
	if err := st.SaveApplicationResources("billing", ApplicationResources{ResourceGroup: g.Name, GroupRevision: stale.Revision}); err == nil {
		t.Fatal("stale group selection accepted")
	}
	changed := st.ResourceGroup(g.Name)
	changed.Requirements = changed.Requirements[:1]
	if err := st.SaveResourceGroup(changed); err == nil {
		t.Fatal("referenced alias removed")
	}
	r := st.ApplicationResources("billing")
	r.ResourceGroup = ""
	r.GroupRevision = 0
	if err := st.SaveApplicationResources("billing", r); err != nil {
		t.Fatal(err)
	}
	if st.ApplicationResources("billing").ResourceGroup != "" {
		t.Fatal("detach failed")
	}
	if c, err := st.sqlBinding("billing", "prod", "db"); err != nil || c.Name != "db-prod" {
		t.Fatal("detach lost chosen bindings", err)
	}
}

func resourceSuccessProof(t *testing.T, st *Store, c *Change, connection string) {
	t.Helper()
	ops, err := parseChangeSQL(c.SQL.Content)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ops {
		ops[i].ChangeID = c.ID
	}
	dest, err := st.sqlBinding(c.Service, "test", c.SQL.ResourceAlias)
	if err != nil || dest.Name != connection {
		t.Fatal("invalid bound proof target", err)
	}
	p := &SQLExecutionPlan{NodeID: "proof", ChangeIDs: []int64{c.ID}, Services: []string{c.Service}, Aliases: []string{c.SQL.ResourceAlias}, Connection: connection, Database: dest.Config["database"], DestinationDigest: sqlDestination(dest), ChangeDigests: []string{digestJSON([]any{c.Service, c.SQL})}, Statements: ops}
	doc, _ := json.Marshal(DeliveryPlan{Environment: Environment{Name: "test"}, SQLPlans: []*SQLExecutionPlan{p}})
	r, err := st.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'proof','success','[]','{}','alice','reviewer',1,1)`, string(doc))
	if err != nil {
		t.Fatal(err)
	}
	run, _ := r.LastInsertId()
	if _, err := st.db.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES(?,?,?,?,?,?,?,'fixture')`, connection, dest.Config["database"], c.ID, sqlChangeDigest(p, c.ID), "success", run, "proof"); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryMultipleResourceTargetsFreezeAndPreserveApproval(t *testing.T) {
	st, m, g := resourceGroupFixture(t)
	env := st.GetEnvironment("test")
	env.Approvers = "reviewer"
	st.UpsertEnvironment(*env)
	changes := []Change{}
	for _, alias := range []string{"db", "extra"} {
		c := &Change{Kind: "mysql", Title: "Fix " + alias, Service: "orders", Owner: "alice", SQL: &SQLChange{ResourceAlias: alias, Content: "UPDATE menus SET name='new' WHERE id=1", MaxAffectedRows: 100}}
		if err := st.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		st.SetChangeReady(c.ID, true)
		target, _ := st.sqlBinding(c.Service, "test", alias)
		resourceSuccessProof(t, st, c, target.Name)
		changes = append(changes, *c)
	}
	scope := &DeliveryScope{Type: "work_items", Changes: changes}
	nodes := []DeliveryNode{{ID: "review", Kind: "approval", Name: "Review", Approvers: []string{"reviewer"}, ApprovalMode: "any"}, {ID: "sql", Kind: "mysql", Name: "SQL"}, {ID: "observe", Kind: "manual", Name: "Observe", Instructions: "Verify"}}
	expanded, targets, err := st.expandDeliveryResourceNodes(nodes, scope, "test")
	if err != nil || len(expanded) != 4 || len(targets) != 2 || expanded[0].ID != "review" || expanded[3].ID != "observe" || targets[0].Connection != "db-dev" || targets[1].Connection != "db-second" {
		t.Fatalf("bad expansion: %v %+v %+v", err, expanded, targets)
	}
	if expanded[1].ID == expanded[2].ID || targets[0].OriginNodeID != "sql" || targets[0].GroupName != g.Name {
		t.Fatal("missing origin / group snapshot")
	}
	again, _, err := st.expandDeliveryResourceNodes(nodes, scope, "test")
	if err != nil || digestJSON(again) != digestJSON(expanded) {
		t.Fatal("non-deterministic expansion")
	}
	tpl := &DeliveryTemplate{Name: "Auto resources", Mode: "fixed", Nodes: []DeliveryNode{nodes[0], {ID: "sql", Kind: "mysql", Name: "SQL", ChangeIDs: []int64{changes[0].ID, changes[1].ID}}, nodes[2]}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{TemplateID: tpl.ID, Environment: "test"}
	p, secrets, err := st.ResolveDelivery(req, m)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatal("offline proven changes should be reusable", err, p.Blockers)
	}
	if len(p.SQLPlans) != 2 || len(p.ResourceTargets) != 2 || len(p.Nodes) != 4 {
		t.Fatal("plans not split")
	}
	token, _ := st.signDeliveryPreflight(req, p, secrets, "alice")
	req.PreflightToken = token
	for _, plan := range p.SQLPlans {
		if len(plan.Applied) != 1 || plan.Mode != "applied" {
			t.Fatal("skip lost after split")
		}
	}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "pending_approval" || len(run.Steps) != 4 {
		t.Fatal("approval bypassed / steps lost")
	}
	oldRevision := run.Plan.ResourceTargets[0].GroupRevision
	g.Description = "changed after preview"
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if recovered, err := st.CreateDeliveryRun(req, "alice", m); err != nil || recovered.ID != run.ID {
		t.Fatal("lost submit must recover original immutable snapshot, not create a new plan", err)
	}
	if st.DeliveryRun(run.ID).Plan.ResourceTargets[0].GroupRevision != oldRevision {
		t.Fatal("historical group snapshot changed")
	}
	// No external MySQL exists at this fixture address; skip requires no connection.
	if _, err := st.executeSQLNode(context.Background(), run, p.SQLPlans[0]); err != nil {
		t.Fatal(err)
	}
	for i := range g.Bindings {
		if g.Bindings[i].Alias == "db" && g.Bindings[i].Environment == "test" {
			g.Bindings[i].Connection = "db-second"
		}
	}
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if _, err := st.executeSQLNode(context.Background(), run, p.SQLPlans[0]); err == nil {
		t.Fatal("changed group target executed old approved plan")
	}
	foreign := DeliveryNode{ID: "foreign", Kind: "mysql", Name: "Foreign", ChangeIDs: []int64{changes[1].ID}}
	if _, _, err := st.expandDeliveryResourceNodes([]DeliveryNode{foreign}, &DeliveryScope{Changes: changes[:1]}, "test"); err == nil {
		t.Fatal("scope guard lost")
	}
}

func TestResourceGroupAPIAuthorizationAndValidation(t *testing.T) {
	st, _, g := resourceGroupFixture(t)
	mux := http.NewServeMux()
	registerResourceGroupRoutes(mux, st)
	body, _ := json.Marshal(g)
	r := httptest.NewRequest("POST", "/api/resource-groups", strings.NewReader(string(body)))
	r.Header.Set("X-User", "member")
	r.Header.Set("X-Role", "member")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("member edited shared group", w.Code)
	}
	copy := *g
	copy.Name = "new-group"
	copy.Revision = 0
	copy.Bindings = append(copy.Bindings, copy.Bindings[0])
	if err := st.SaveResourceGroup(&copy); err == nil {
		t.Fatal("duplicate environment binding accepted")
	}
	if _, err := st.db.Exec(`INSERT INTO resource_applications(name) VALUES('database-only')`); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("database-only", ApplicationResources{ResourceGroup: g.Name}); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	routes := Routes(st, NewBuilder(st, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir()))
	w = httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest("GET", "/api/services/database-only/resources", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"resource_group":"business"`) {
		t.Fatal("resource-only application cannot read group bindings", w.Code, w.Body.String())
	}
}

func TestResourceGroupValidationExplainsSpecificMissingField(t *testing.T) {
	st, _, _ := resourceGroupFixture(t)
	for _, tc := range []struct {
		name    string
		group   ResourceGroup
		message string
	}{
		{"missing resource", ResourceGroup{Name: "empty-group"}, "请点击“添加资源”，至少添加一项资源后再保存"},
		{"invalid name", ResourceGroup{Name: "Bad Name"}, "资源组标识需以小写字母开头"},
		{"long description", ResourceGroup{Name: "too-long", Description: strings.Repeat("x", 1001)}, "资源组说明不能超过 1000 字节"},
		{"too many resources", ResourceGroup{Name: "too-many", Requirements: make([]ApplicationResourceRequirement, 31)}, "最多添加 30 项资源"},
		{"too many bindings", ResourceGroup{Name: "too-many-bindings", Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: make([]ApplicationEnvironmentResource, 301)}, "最多添加 300 个环境绑定"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := st.SaveResourceGroup(&tc.group)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected actionable validation %q, got %v", tc.message, err)
			}
			if st.ResourceGroup(tc.group.Name) != nil {
				t.Fatal("invalid resource group was saved")
			}
		})
	}
}

func TestNacosGroupDocumentsSplitAndDriftStops(t *testing.T) {
	st, m, _ := deliveryFixture(t)
	var mu sync.Mutex
	contents := map[string]string{"test-a.yaml": "feature: false\n", "test-b.yaml": "feature: false\n"}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nacos/v1/cs/configs" {
			http.NotFound(w, r)
			return
		}
		r.ParseForm()
		id := r.Form.Get("dataId")
		mu.Lock()
		defer mu.Unlock()
		before, ok := contents[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			w.Write([]byte(before))
			return
		}
		if r.Header.Get("casMd5") != nacosMD5(before) {
			http.Error(w, "conflict", 409)
			return
		}
		posts++
		contents[id] = r.Form.Get("content")
		w.Write([]byte("true"))
	}))
	defer server.Close()
	if err := st.UpsertConnection(ResourceConnection{Name: "config-center", Kind: "nacos", Endpoint: server.URL + "/nacos", Config: map[string]string{"nacos_auth": "none", "nacos_version": "2.4"}}); err != nil {
		t.Fatal(err)
	}
	g := &ResourceGroup{Name: "config-group", Requirements: []ApplicationResourceRequirement{{Alias: "config-a", Kind: "nacos"}, {Alias: "config-b", Kind: "nacos"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "config-a", Environment: "test", Connection: "config-center", Config: map[string]string{"data_id": "test-a.yaml", "group": "DEFAULT_GROUP"}}, {Alias: "config-b", Environment: "test", Connection: "config-center", Config: map[string]string{"data_id": "test-b.yaml", "group": "DEFAULT_GROUP"}}}}
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{ResourceGroup: g.Name}); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, alias := range []string{"config-a", "config-b"} {
		c := &Change{Kind: "nacos", Title: alias, Service: "orders", Nacos: &NacosChange{ResourceAlias: alias, DataID: "fallback.yaml", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []NacosEntry{{Key: "feature", Action: "set", Value: "true"}}}}
		if err := st.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		st.SetChangeReady(c.ID, true)
		ids = append(ids, c.ID)
	}
	tpl := &DeliveryTemplate{Name: "Nacos documents", Mode: "fixed", Nodes: []DeliveryNode{{ID: "config", Kind: "nacos", Name: "Configure", ChangeIDs: ids}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{TemplateID: tpl.ID, Environment: "test"}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Steps) != 2 || len(run.Plan.NacosPlans) != 2 || run.Plan.NacosPlans[0].Configs[0].DataID != "test-a.yaml" {
		t.Fatal("documents not resolved and split")
	}
	mu.Lock()
	writes := posts
	mu.Unlock()
	if writes != 0 {
		t.Fatal("preview published config")
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	writes = posts
	mu.Unlock()
	if writes != 1 {
		t.Fatal("first document did not execute once")
	}
	for i := range g.Bindings {
		if g.Bindings[i].Alias == "config-b" {
			g.Bindings[i].Config["data_id"] = "test-a.yaml"
		}
	}
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if failed, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil && (failed == nil || failed.Status != "failed" || !strings.Contains(failed.Steps[1].Detail, "绑定变化")) {
		t.Fatal("changed target executed under old approval")
	}
	mu.Lock()
	writes = posts
	mu.Unlock()
	if writes != 1 {
		t.Fatal("target drift caused second write")
	}
}
