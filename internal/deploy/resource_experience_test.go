package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestScopedNacosResourcesNeverExpandIntoOtherEnvironments(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	for _, name := range []string{"test-nacos", "prod-nacos"} {
		if err := s.UpsertConnection(ResourceConnection{Name: name, Kind: "nacos", Endpoint: "http://127.0.0.1:1", Config: map[string]string{"nacos_auth": "none"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range []string{"test", "prod"} {
		_, _ = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "environment_nacos:"+env, env+"-nacos")
	}
	in := ApplicationResources{CompositionVersion: 2, NacosConfigs: []ApplicationNacosConfig{{Alias: "app", Environment: "test", Connection: "test-nacos", Group: "TEST_GROUP", DataID: "app-test.yaml"}}}
	if err := s.SaveApplicationResources("orders", in); err != nil {
		t.Fatal(err)
	}
	got := s.ApplicationResources("orders")
	if len(got.Bindings) != 1 || got.Bindings[0].Environment != "test" {
		t.Fatalf("scope expanded: %+v", got)
	}
	in.NacosConfigs = append(in.NacosConfigs, ApplicationNacosConfig{Alias: "app", Environment: "prod", Connection: "prod-nacos", Group: "PROD_GROUP", DataID: "app-prod.yaml"})
	if err := s.SaveApplicationResources("orders", in); err != nil {
		t.Fatal(err)
	}
	got = s.ApplicationResources("orders")
	if len(got.Requirements) != 1 || len(got.Bindings) != 2 || got.Bindings[1].Config["data_id"] == got.Bindings[0].Config["data_id"] {
		t.Fatal("lost independent mapping", got)
	}
	in.NacosConfigs[0].Environment = ""
	if err := s.SaveApplicationResources("orders", in); err == nil {
		t.Fatal("accepted unscoped new reference")
	}
}
func TestLegacyNacosScopeMigrationFreezesExistingTargets(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	_ = s.UpsertConnection(ResourceConnection{Name: "config", Kind: "nacos", Endpoint: "http://127.0.0.1:1", Config: map[string]string{"nacos_auth": "none"}})
	_, _ = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('environment_nacos:test','config')`)
	raw, _ := json.Marshal(ApplicationResources{CompositionVersion: 2, NacosConfigs: []ApplicationNacosConfig{{Alias: "config", DataID: "shared.yaml", Group: "COMMON"}}})
	_, _ = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('application_resources_v2:orders',?)`, string(raw))
	if err := s.migrateLegacyNacosResourceScopes(); err != nil {
		t.Fatal(err)
	}
	first := s.ApplicationResources("orders")
	if len(first.Bindings) != 1 || first.Bindings[0].Environment != "test" {
		t.Fatal(first)
	}
	_, _ = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('environment_nacos:prod','config')`)
	if err := s.migrateLegacyNacosResourceScopes(); err != nil {
		t.Fatal(err)
	}
	second := s.ApplicationResources("orders")
	if len(second.Bindings) != 1 || second.Revision != first.Revision {
		t.Fatal("later environment expanded historical scope", second)
	}
}
func TestApplicationResourceRevisionRejectsStaleWindowAtomically(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	initial := s.ApplicationResources("orders")
	revision := initial.Revision
	makeDraft := func(alias string) ApplicationResources {
		return ApplicationResources{CompositionVersion: 2, Revision: revision, ExpectedRevision: &revision, OwnRequirements: []ApplicationResourceRequirement{{Alias: alias, Kind: "mysql"}}, OwnBindings: []ApplicationEnvironmentResource{{Alias: alias, Environment: "test", Connection: "db-dev", Config: map[string]string{"database": "dev_db"}}}}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, alias := range []string{"first", "second"} {
		wg.Add(1)
		go func(alias string) { defer wg.Done(); results <- s.SaveApplicationResources("orders", makeDraft(alias)) }(alias)
	}
	wg.Wait()
	close(results)
	passed, failed := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else {
			failed++
		}
	}
	if passed != 1 || failed != 1 {
		t.Fatalf("CAS not atomic %d/%d", passed, failed)
	}
	got := s.ApplicationResources("orders")
	if got.Revision != revision+1 || len(got.Requirements) != 1 {
		t.Fatal(got)
	}
}
func TestResourceFailuresAreClassifiedWithoutSecrets(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{{&mysql.MySQLError{Number: 1045, Message: "PASSWORD-MUST-NOT-LEAK"}, "authentication_failed"}, {&mysql.MySQLError{Number: 1049, Message: "SQL-MUST-NOT-LEAK"}, "database_missing"}, {syscall.ECONNREFUSED, "connection_refused"}, {context.DeadlineExceeded, "timeout"}, {errors.New("TOKEN-MUST-NOT-LEAK"), "connection_failed"}}
	for _, tt := range tests {
		got := resourceFailure(fmt.Errorf("wrapped: %w", tt.err))
		raw, _ := json.Marshal(got)
		if got.Code != tt.code || strings.Contains(string(raw), "MUST-NOT-LEAK") {
			t.Fatal(got)
		}
	}
}
func TestConnectionEvidenceExpiresWhenCredentialsOrTargetChange(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	c := s.GetConnection("db-dev")
	check, err := s.recordConnectionCheck(c, "owner", "实例连接通过；未核验表权限", nil)
	if err != nil || check.Status != "passed" {
		t.Fatal(check, err)
	}
	find := func() *ResourceConnection {
		for _, value := range s.Connections() {
			if value.Name == c.Name {
				return &value
			}
		}
		return nil
	}
	if find().LastCheck.Status != "passed" {
		t.Fatal("missing persisted evidence")
	}
	c.Password = "new-private-password"
	if err := s.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	got := find()
	if got.LastCheck.Status != "stale" {
		t.Fatal(got)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), c.Password) {
		t.Fatal("credential leaked")
	}
}
func TestResourceBatchChecksExplicitScopeAndPersistOnlySavedTargets(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	value := s.ApplicationResources("orders")
	calls := []string{}
	var mu sync.Mutex
	probe := func(ctx context.Context, kind string, b ApplicationEnvironmentResource) ResourceCheck {
		mu.Lock()
		calls = append(calls, b.Environment)
		mu.Unlock()
		return ResourceCheck{Status: "passed", Code: "target_verified", Message: "目标通过，未核验写权限"}
	}
	if _, err := s.checkApplicationResources(context.Background(), "orders", "owner", value, nil, nil, probe); err == nil {
		t.Fatal("implicit all-environment probe")
	}
	result, err := s.checkApplicationResources(context.Background(), "orders", "owner", value, []string{"test"}, nil, probe)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || len(calls) != 2 {
		t.Fatal(result, calls)
	}
	for _, env := range calls {
		if env != "test" {
			t.Fatal("probed unselected environment")
		}
	}
	var current ApplicationResources
	current = s.ApplicationResources("orders")
	s.attachApplicationResourceChecks("orders", &current)
	if len(current.Checks) != 2 {
		t.Fatal("missing saved evidence", current.Checks)
	}
	draft := value
	draft.Bindings = append([]ApplicationEnvironmentResource{}, value.Bindings...)
	draft.Bindings[0].Connection = "db-prod"
	_, _ = s.checkApplicationResources(context.Background(), "orders", "owner", draft, []string{"test"}, nil, probe)
	current = s.ApplicationResources("orders")
	s.attachApplicationResourceChecks("orders", &current)
	for _, c := range current.Checks {
		if c.Connection == "db-prod" && c.Environment == "test" {
			t.Fatal("draft test replaced saved target evidence")
		}
	}
}
func TestDependencyDiscoveryKeepsEnvironmentsAndSecretsSeparate(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	secret := "UNIQUE-DO-NOT-RETURN-CREDENTIAL"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("discovery attempted write")
			w.WriteHeader(405)
			return
		}
		id := r.URL.Query().Get("dataId")
		if id == "orders.yaml" {
			w.WriteHeader(404)
			return
		}
		if id == "redis.yaml" {
			fmt.Fprint(w, "spring:\n  redis:\n    host: private-redis\n    database: ${company.redisDatasource}\n    password: "+secret)
			return
		}
		if id != "orders-test.yaml" {
			t.Error("read wrong environment", id)
		}
		fmt.Fprint(w, "company:\n  redisDatasource: 4\nspring:\n  datasource:\n    dynamic:\n      datasource:\n        orders_db:\n          url: jdbc:mysql://127.0.0.1:3306/orders_test\n          username: fixture\n          password: "+secret+"\n  kafka:\n    bootstrap-servers: private-kafka:9092\neasy-es:\n  address: private-es:9200\n")
	}))
	defer upstream.Close()
	for _, c := range []ResourceConnection{{Name: "nacos", Kind: "nacos", Endpoint: upstream.URL, Config: map[string]string{"nacos_auth": "none", "namespace": "test-space"}}, {Name: "mysql", Kind: "mysql", Endpoint: "127.0.0.1:3306", Username: "fixture", Password: secret}} {
		if err := s.UpsertConnection(c); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap := "spring:\n  application:\n    name: orders\n  profiles:\n    active: '@env@'\n  cloud:\n    nacos:\n      config:\n        namespace: '@nacos.namespace@'\n        group: '@nacos.config.group@'\n        file-extension: yaml\n        shared-configs:\n          - data-id: redis.yaml\n            group: COMMON_GROUP\n"
	pom := "<project><profiles><profile><id>test</id><properties><env>test</env><nacos.namespace>test-space</nacos.namespace><nacos.config.group>TEST_GROUP</nacos.config.group></properties></profile></profiles></project>"
	got, err := s.discoverApplicationResources(context.Background(), "orders", resourceDiscoveryRequest{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos"}, BootstrapContent: bootstrap, PomContent: pom})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "password") {
		t.Fatal("preview leaked source credentials", string(encoded))
	}
	foundDB, foundScoped, foundUnsupported := false, false, false
	for _, item := range got.Items {
		if item.Environment != "test" {
			t.Fatal("expanded scope")
		}
		if item.Kind == "mysql" {
			foundDB = item.Applyable && item.Database == "orders_test" && item.Connection == "mysql"
		}
		if item.Alias == "nacos_app" {
			foundScoped = item.Group == "TEST_GROUP" && item.DataID == "orders-test.yaml"
		}
		if item.Kind == "kafka" {
			foundUnsupported = !item.Applyable && !item.Supported
		}
	}
	if !foundDB || !foundScoped || !foundUnsupported {
		t.Fatal(got)
	}
}
func TestDiscoveryXXLJobFromNacosProperties(t *testing.T) {
	store, _, _ := deliveryFixture(t)
	const endpoint = "https://scheduler.example.invalid/estate-job"
	const secret = "fixture-devops-secret-never-present-in-preview"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataID := r.URL.Query().Get("dataId")
		if r.Method != http.MethodGet || (dataID != "jobs.properties" && dataID != "custom-jobs.properties") {
			t.Errorf("unexpected Nacos request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "scheduler.endpoint=%s/\nxxl.job.devops-token=%s\n", endpoint, secret)
		if dataID == "custom-jobs.properties" {
			fmt.Fprint(w, "xxl.job.admin.addresses=https://unrelated.example.invalid/estate-job\norders.scheduler.admin-url=${scheduler.endpoint}\nxxl.job.executor.appName=orders-jobs\n")
			return
		}
		fmt.Fprint(w, "xxl.job.admin.addresses=${scheduler.endpoint}\nxxl.job.executor.appname=orders-jobs\n")
	}))
	defer upstream.Close()
	for _, connection := range []ResourceConnection{
		{Name: "nacos", Kind: "nacos", Endpoint: upstream.URL, Config: map[string]string{"nacos_auth": "none"}},
		{Name: "scheduler-test", Kind: xxlJobKind, Endpoint: endpoint, Password: secret, Config: map[string]string{"environment": "test"}},
		{Name: "scheduler-prod", Kind: xxlJobKind, Endpoint: endpoint, Password: secret, Config: map[string]string{"environment": "prod"}},
	} {
		if err := store.UpsertConnection(connection); err != nil {
			t.Fatal(err)
		}
	}
	for _, scenario := range []struct {
		name, dataID, addressKey, state string
	}{
		{"default key", "jobs.properties", "", "matched"},
		{"custom key overrides default", "custom-jobs.properties", "orders.scheduler.admin-url", "matched"},
		{"missing custom key does not fall back", "jobs.properties", "orders.scheduler.missing", "unresolved"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := resourceDiscoveryRequest{
				Environments: []string{"test"}, Connections: map[string]string{"test": "nacos"},
				BootstrapContent: "spring:\n  application:\n    name: orders\n", Adapter: "custom", ConfigDataID: scenario.dataID,
				ConfigGroup: "DEFAULT_GROUP", XXLJobAddressKey: scenario.addressKey,
			}
			result, err := store.discoverApplicationResources(context.Background(), "orders", request)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "scheduler-prod") {
				t.Fatal("preview exposed credential or selected another environment")
			}
			for _, candidate := range result.Items {
				if candidate.Kind != xxlJobKind {
					continue
				}
				if candidate.State != scenario.state {
					t.Fatalf("unexpected candidate: %+v", candidate)
				}
				if scenario.state == "unresolved" {
					if candidate.Applyable || candidate.Binding != nil {
						t.Fatalf("missing property generated a binding: %+v", candidate)
					}
					return
				}
				if !candidate.Applyable || candidate.Binding == nil || candidate.Binding.Connection != "scheduler-test" ||
					candidate.Binding.Config["app_name"] != "orders-jobs" || len(candidate.Binding.Config) != 1 {
					t.Fatalf("unexpected binding: %+v", candidate)
				}
				return
			}
			t.Fatal("no XXL-JOB candidate in preview")
		})
	}
}

func TestResourceDiscoveryCannotReadOutsideManagedWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.yml")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "bootstrap.yml")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../private.yml", outside, "bootstrap.yml"} {
		if _, err := readResourceSourceFile(root, path); err == nil {
			t.Fatal("escaped source scope", path)
		}
	}
}

func TestResourceConnectionConfigurationFreezesSecretsAndTargetRevisions(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	if err := s.UpsertConnection(ResourceConnection{Name: "source-db", Kind: "mysql", Endpoint: "127.0.0.1:3306", Username: "private-user", Password: "PRIVATE-PASSWORD", Config: map[string]string{"tls": "false"}}); err != nil {
		t.Fatal(err)
	}
	req := resourceConnectionConfiguration{Name: "new-db", Kind: "mysql", Endpoint: "127.0.0.2:3306", Config: map[string]string{"tls": "false"}, Revision: "missing", CredentialConnection: "source-db"}
	target, err := s.prepareResourceConnectionConfiguration(req, false)
	if err != nil {
		t.Fatal(err)
	}
	req.CredentialRevision = s.connectionCheckFingerprint(s.GetConnection("source-db"))
	if target.Password != "PRIVATE-PASSWORD" {
		t.Fatal("credential snapshot lost")
	}
	if err = s.UpsertConnection(target); err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(s.connectionMetadata("new-db"))
	if strings.Contains(string(public), "PRIVATE-PASSWORD") || strings.Contains(string(public), "private-user") {
		t.Fatal("metadata leaked credentials")
	}
	if _, err = s.prepareResourceConnectionConfiguration(req, true); err == nil {
		t.Fatal("stale target accepted")
	}
	req.Revision = s.connectionCheckFingerprint(s.GetConnection("new-db"))
	source := s.GetConnection("source-db")
	source.Password = "ROTATED"
	if err = s.UpsertConnection(*source); err != nil {
		t.Fatal(err)
	}
	if _, err = s.prepareResourceConnectionConfiguration(req, true); err == nil {
		t.Fatal("changed credential source accepted")
	}
	if s.GetConnection("new-db").Password != "PRIVATE-PASSWORD" {
		t.Fatal("source rotation silently changed copied credentials")
	}
	req.Config["password"] = "not-allowed"
	if _, err = s.prepareResourceConnectionConfiguration(req, false); err == nil {
		t.Fatal("secret config accepted")
	}
}
func TestResourceBindingPatchPreservesOtherEnvironmentAndDetectsDrift(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	value := ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: "db-dev", Config: map[string]string{"database": "test_db"}}, {Alias: "db", Environment: "prod", Connection: "db-dev", Config: map[string]string{"database": "prod_db"}}}}
	if err := s.SaveApplicationResources("orders", value); err != nil {
		t.Fatal(err)
	}
	current := s.ApplicationResources("orders")
	patch := resourceBindingPatch{Revision: current.Revision, Environment: "test", Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: "db-dev", Config: map[string]string{"database": "changed"}}}, ConnectionRevisions: map[string]string{"db-dev": s.connectionCheckFingerprint(s.GetConnection("db-dev"))}}
	next, err := s.prepareResourceBindingPatch("orders", patch, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveApplicationResources("orders", next); err != nil {
		t.Fatal(err)
	}
	got := s.ApplicationResources("orders")
	for _, b := range got.Bindings {
		if b.Environment == "prod" && b.Config["database"] != "prod_db" {
			t.Fatal("changed another environment")
		}
	}
	if _, err = s.prepareResourceBindingPatch("orders", patch, true); err == nil {
		t.Fatal("stale app revision accepted")
	}
	patch.Revision = got.Revision
	patch.Bindings[0].Environment = "prod"
	if _, err = s.prepareResourceBindingPatch("orders", patch, true); err == nil {
		t.Fatal("scope substitution accepted")
	}
	patch.Bindings[0].Environment = "test"
	patch.ConnectionRevisions["db-dev"] = "changed"
	if _, err = s.prepareResourceBindingPatch("orders", patch, true); err == nil {
		t.Fatal("stale target accepted")
	}
}
func TestRedisMultipleExactPrefixesRemainBoundedAndLegacySnapshotsStable(t *testing.T) {
	c := &ResourceConnection{Kind: "redis", Endpoint: "127.0.0.1:6379", Config: map[string]string{"key_prefixes": `["menu:","cache:orders:"]`}}
	if err := validateRedisConnection(c); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"GET menu:one", "GET cache:orders:two", "SCAN 0 MATCH cache:orders:* COUNT 10"} {
		commands, err := parseRedisCommandText(text, redisReadKeys)
		if err != nil {
			t.Fatal(err)
		}
		if err = validateRedisReadScope(commands, c); err != nil {
			t.Fatal(text, err)
		}
	}
	for _, text := range []string{"GET other:one", "SCAN 0 MATCH * COUNT 10", "SCAN 0 MATCH cache:* COUNT 10"} {
		commands, err := parseRedisCommandText(text, redisReadKeys)
		if err == nil && validateRedisReadScope(commands, c) == nil {
			t.Fatal("scope widened", text)
		}
	}
	prefixes, _ := redisAllowedPrefixes(c)
	if redisKeyInPrefixes("menu:", prefixes, false) || redisKeyInPrefixes("menu:*", prefixes, false) {
		t.Fatal("write scope too broad")
	}
	for _, raw := range []string{`[]`, `[""]`, `["*"]`, `["valid:",""]`} {
		c.Config["key_prefixes"] = raw
		if validateRedisConnection(c) == nil {
			t.Fatal("unsafe allowlist accepted", raw)
		}
	}
	c.Config = map[string]string{"key_prefix": "legacy:"}
	if err := validateRedisConnection(c); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(RedisChangePlan{Prefix: "legacy:"})
	if strings.Contains(string(raw), "prefixes") {
		t.Fatal("legacy snapshot shape changed")
	}
}
func TestLegacyUnboundNacosReferenceDoesNotExpandOnLaterRestart(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	raw, _ := json.Marshal(ApplicationResources{CompositionVersion: 2, NacosConfigs: []ApplicationNacosConfig{{Alias: "orphan", DataID: "orphan.yaml", Group: "DEFAULT_GROUP"}}})
	_, _ = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('application_resources_v2:orders',?)`, string(raw))
	if err := s.migrateLegacyNacosResourceScopes(); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertConnection(ResourceConnection{Name: "later", Kind: "nacos", Endpoint: "http://127.0.0.1:1", Config: map[string]string{"nacos_auth": "none"}}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('environment_nacos:test','later')`)
	if err := s.migrateLegacyNacosResourceScopes(); err != nil {
		t.Fatal(err)
	}
	var stored string
	var got ApplicationResources
	s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key='application_resources_v2:orders'`).Scan(&stored)
	json.Unmarshal([]byte(stored), &got)
	if got.NacosScopeVersion != 1 || got.NacosConfigs[0].Environment != "" {
		t.Fatal("unbound historic reference silently expanded", got.NacosConfigs)
	}
}
func TestScopedCatalogReferencesProtectConnectionAndEnvironmentDeletion(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	if err := s.UpsertEnvironment(Environment{Name: "catalog-only", DisplayName: "目录专用环境"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertConnection(ResourceConnection{Name: "catalog-only", Kind: "nacos", Endpoint: "http://127.0.0.1:1", Config: map[string]string{"nacos_auth": "none"}}); err != nil {
		t.Fatal(err)
	}
	in := ApplicationResources{CompositionVersion: 2, NacosConfigs: []ApplicationNacosConfig{{Alias: "config", Environment: "catalog-only", Connection: "catalog-only", DataID: "app.yaml", Group: "DEFAULT_GROUP"}}}
	if err := s.SaveApplicationResources("orders", in); err != nil {
		t.Fatal(err)
	}
	if s.DeleteConnection("catalog-only") == nil {
		t.Fatal("referenced connection deleted")
	}
	if s.DeleteEnvironment("catalog-only") == nil {
		t.Fatal("referenced environment deleted")
	}
}
