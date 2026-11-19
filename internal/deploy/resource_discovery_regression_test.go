package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func discoveryRegressionFixture(t *testing.T, configs map[string]string) (*Store, *httptest.Server, *atomic.Int32) {
	t.Helper()
	s, _, _ := deliveryFixture(t)
	reads := &atomic.Int32{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("discovery attempted side effect")
			w.WriteHeader(405)
			return
		}
		reads.Add(1)
		key := r.URL.Query().Get("tenant") + "/" + r.URL.Query().Get("group") + "/" + r.URL.Query().Get("dataId")
		raw, ok := configs[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, raw)
	}))
	t.Cleanup(up.Close)
	for _, env := range []string{"dev", "test", "prod"} {
		if s.GetEnvironment(env) == nil {
			if err := s.UpsertEnvironment(Environment{Name: env, DisplayName: env}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.UpsertConnection(ResourceConnection{Name: "nacos-" + env, Kind: "nacos", Endpoint: up.URL, Config: map[string]string{"nacos_auth": "none", "namespace": env + "-space"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertConnection(ResourceConnection{Name: "db-matched", Kind: "mysql", Endpoint: "127.0.0.1:3306", Username: "fixture", Password: "PRIVATE-DISCOVERY-SECRET"}); err != nil {
		t.Fatal(err)
	}
	return s, up, reads
}
func discoverySource(name, endpoint string) string {
	return "spring:\n  application:\n    name: " + name + "\n  profiles:\n    active: '@env@'\n  cloud:\n    nacos:\n      config:\n        server-addr: " + endpoint + "\n        namespace: '@nacos.namespace@'\n        group: G\n        prefix: '${spring.application.name}'\n        file-extension: yaml\n"
}
func discoveryProfiles() string {
	return "<project><profiles><profile><id>dev</id><properties><env>dev</env><nacos.namespace>dev-space</nacos.namespace></properties></profile><profile><id>test</id><properties><env>test</env><nacos.namespace>test-space</nacos.namespace></properties></profile></profiles></project>"
}
func discoveryDatabase(alias, database string) string {
	return "spring:\n  datasource:\n    dynamic:\n      datasource:\n        " + alias + ":\n          url: jdbc:mysql://127.0.0.1:3306/" + database + "\n          username: fixture\n          password: PRIVATE-DISCOVERY-SECRET\n"
}

func TestDiscoveryRuntimeMappingUsesArtifactModuleAndPreservesSource(t *testing.T) {
	s, up, reads := discoveryRegressionFixture(t, map[string]string{"test-space/G/runtime-orders-test.yaml": discoveryDatabase("dataIntelligenceMysql", "orders_test")})
	svc := s.GetService("orders")
	svc.ArtifactGlob = "module/target/orders.jar"
	if err := s.UpsertService(*svc); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(s.dataDir, "workspace", "orders")
	relative := "module/src/main/resources/bootstrap.yml"
	file := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	source := discoverySource("runtime-orders", up.URL)
	os.WriteFile(file, []byte(source), 0600)
	os.WriteFile(filepath.Join(root, "pom.xml"), []byte(discoveryProfiles()), 0600)
	req := resourceDiscoveryRequest{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-test"}}
	if _, err := s.discoverApplicationResources(context.Background(), "orders", req); err == nil || reads.Load() != 0 {
		t.Fatal("unmapped runtime read")
	}
	req.RuntimeName = "runtime-orders"
	got, err := s.discoverApplicationResources(context.Background(), "orders", req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range got.Items {
		if item.Kind == "mysql" {
			found = item.Applyable && item.Alias == "data_intelligence_mysql" && item.OriginalAlias == "dataIntelligenceMysql" && item.Database == "orders_test" && item.Requirement.Sources["test"].File == relative
		}
	}
	if !found || got.Origin.RuntimeName != "runtime-orders" || got.Origin.Artifact != svc.ArtifactGlob {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE-DISCOVERY-SECRET") {
		t.Fatal("secret escaped")
	}
	after, _ := os.ReadFile(file)
	if string(after) != source {
		t.Fatal("source rewritten")
	}
	wrong := filepath.Join(root, "unrelated/bootstrap.yml")
	os.MkdirAll(filepath.Dir(wrong), 0700)
	os.WriteFile(wrong, []byte(source), 0600)
	req.BootstrapPath = "unrelated/bootstrap.yml"
	at := reads.Load()
	if _, err = s.discoverApplicationResources(context.Background(), "orders", req); err == nil || reads.Load() != at {
		t.Fatal("runtime mapping escaped artifact module")
	}
	req.SourceFormat = "properties"
	if _, err = s.discoverApplicationResources(context.Background(), "orders", req); err == nil || reads.Load() != at {
		t.Fatal("properties selector bypassed artifact module ownership")
	}
}

func TestDiscoveryFirstAccessRequiresEnvironmentRegistrationAndSourceTarget(t *testing.T) {
	s, up, reads := discoveryRegressionFixture(t, map[string]string{"test-space/G/orders-test.yaml": "feature: true"})
	req := resourceDiscoveryRequest{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-test"}, BootstrapContent: discoverySource("orders", up.URL), PomContent: discoveryProfiles(), RequireEnvironmentScope: true}
	if _, err := s.discoverApplicationResources(context.Background(), "orders", req); err == nil || reads.Load() != 0 {
		t.Fatal("unregistered connection accessed")
	}
	if err := s.UpsertService(Service{Name: "registered"}); err != nil {
		t.Fatal(err)
	}
	resources := ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "config", Kind: "nacos"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "config", Environment: "test", Connection: "nacos-test", Config: map[string]string{"data_id": "existing.yaml", "group": "G"}}, {Alias: "config", Environment: "prod", Connection: "nacos-prod", Config: map[string]string{"data_id": "existing.yaml", "group": "G"}}}}
	if err := s.SaveApplicationResources("registered", resources); err != nil {
		t.Fatal(err)
	}
	if _, err := s.discoverApplicationResources(context.Background(), "orders", req); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []resourceDiscoveryRequest{
		{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-prod"}, BootstrapContent: discoverySource("orders", up.URL), PomContent: discoveryProfiles(), RequireEnvironmentScope: true},
		{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-test"}, BootstrapContent: discoverySource("orders", "http://127.0.0.2:9999"), PomContent: discoveryProfiles(), RequireEnvironmentScope: true},
		{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-test"}, Profiles: map[string]string{"test": "prod"}, BootstrapContent: discoverySource("orders", up.URL), PomContent: discoveryProfiles(), RequireEnvironmentScope: true},
	} {
		at := reads.Load()
		if _, err := s.discoverApplicationResources(context.Background(), "orders", bad); err == nil || reads.Load() != at {
			t.Fatal("source/environment boundary escaped")
		}
	}
	mux := http.NewServeMux()
	registerResourceDiscoveryAccessRoutes(mux, s)
	request := httptest.NewRequest("GET", "/api/services/orders/resources/discovery-connections?environment=test", nil)
	request.Header.Set("X-Role", "admin")
	request.Header.Set("X-User", "alice")
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, request)
	if out.Code != 200 || strings.Contains(out.Body.String(), "nacos-prod") || strings.Contains(out.Body.String(), "PRIVATE-DISCOVERY-SECRET") || !strings.Contains(out.Body.String(), "nacos-test") {
		t.Fatal(out.Code, out.Body)
	}
}

func TestDiscoveryFixedPropertiesAndSingleFlinkJob(t *testing.T) {
	common := "common:\n  mysql:\n    hostname: 127.0.0.1\n    port: 3306\n    username: fixture\n    password: PRIVATE-DISCOVERY-SECRET\njobs:\n  chosen:\n    mysql:\n      database: orders_test\n  unrelated:\n    mysql:\n      database: forbidden_other_job\n"
	s, up, _ := discoveryRegressionFixture(t, map[string]string{"test-space/DI_GROUP/fixed.yaml": common, "test-space/DI_GROUP/service.yaml": "spring:\n  datasource:\n    url: jdbc:mysql://127.0.0.1:3306/primary_db\n    username: fixture\n    password: PRIVATE-DISCOVERY-SECRET\nhouse-view:\n  data-source:\n    dictionary-schema: dictionary_test\n"})
	req := resourceDiscoveryRequest{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-test"}, SourceFormat: "properties", SourceName: "nacos-bootstrap.properties", BootstrapContent: "nacos.server=" + up.URL + "\nnacos.namespace=@nacos.namespace@\n", PomContent: discoveryProfiles(), Adapter: "flink", ConfigDataID: "fixed.yaml", ConfigGroup: "DI_GROUP"}
	got, err := s.discoverApplicationResources(context.Background(), "orders", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Jobs) != 2 {
		t.Fatal(got)
	}
	for _, i := range got.Items {
		if i.Kind == "mysql" {
			t.Fatal("all jobs expanded without selection")
		}
	}
	req.Job = "chosen"
	got, err = s.discoverApplicationResources(context.Background(), "orders", req)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, i := range got.Items {
		if i.Kind == "mysql" {
			count++
			if !i.Applyable || i.Database != "orders_test" {
				t.Fatal(i)
			}
		}
	}
	if count != 1 {
		t.Fatal(got)
	}
	req.Adapter, req.Job, req.ConfigDataID = "custom", "", "service.yaml"
	got, err = s.discoverApplicationResources(context.Background(), "orders", req)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, i := range got.Items {
		if i.Kind == "mysql" && i.Applyable {
			found[i.Database] = true
		}
	}
	if !found["primary_db"] || !found["dictionary_test"] || got.Origin.File != "nacos-bootstrap.properties" {
		t.Fatal(got)
	}
	req.SourceName = "../../private.properties"
	if _, err = s.discoverApplicationResources(context.Background(), "orders", req); err == nil {
		t.Fatal("uploaded filename accepted path")
	}
}

func TestDiscoveryCoverageNeverGuessesMissingSourceAndNeverHidesBoundFailure(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			configs := map[string]string{"test-space/G/orders-test.yaml": discoveryDatabase("onlyTest", "test_db")}
			if !missing {
				configs["dev-space/G/orders-dev.yaml"] = "feature: true"
			}
			s, up, _ := discoveryRegressionFixture(t, configs)
			got, err := s.discoverApplicationResources(context.Background(), "orders", resourceDiscoveryRequest{Environments: []string{"dev", "test"}, Connections: map[string]string{"dev": "nacos-dev", "test": "nacos-test"}, BootstrapContent: discoverySource("orders", up.URL), PomContent: discoveryProfiles()})
			if err != nil {
				t.Fatal(err)
			}
			var req ApplicationResourceRequirement
			for _, i := range got.Items {
				if i.Kind == "mysql" {
					req = *i.Requirement
				}
			}
			want := "not_applicable"
			if missing {
				want = "pending_source"
			}
			if req.Sources["dev"].State != want {
				t.Fatal(req)
			}
			resources := ApplicationResources{CompositionVersion: 2, OwnRequirements: []ApplicationResourceRequirement{req, {Alias: "legacy", Kind: "mysql"}}}
			if err := s.SaveApplicationResources("orders", resources); err != nil {
				t.Fatal(err)
			}
			saved := s.ApplicationResources("orders")
			if saved.Requirements[0].Sources["dev"].State != want {
				t.Fatal("lost saved evidence")
			}
			probes := 0
			probe := func(context.Context, string, ApplicationEnvironmentResource) ResourceCheck {
				probes++
				return ResourceCheck{Status: "failed", Code: "connection_refused"}
			}
			checks, err := s.checkApplicationResources(context.Background(), "orders", "alice", saved, []string{"dev"}, nil, probe)
			if err != nil {
				t.Fatal(err)
			}
			if checks[0].Status != want || checks[1].Status != "unbound" || probes != 0 {
				t.Fatal(checks)
			}
			resources.OwnBindings = []ApplicationEnvironmentResource{{Alias: req.Alias, Environment: "dev", Connection: "db-matched", Config: map[string]string{"database": "test_db"}}}
			if err := s.SaveApplicationResources("orders", resources); err != nil {
				t.Fatal(err)
			}
			checks, err = s.checkApplicationResources(context.Background(), "orders", "alice", s.ApplicationResources("orders"), []string{"dev"}, []string{req.Alias}, probe)
			if err != nil || checks[0].Status != "failed" || probes != 1 {
				t.Fatal("source observation hid bound target failure", checks, err)
			}
		})
	}
}

func TestDiscoveryLocalProfileAndFlinkDatabaseOverride(t *testing.T) {
	s, up, _ := discoveryRegressionFixture(t, map[string]string{"test-space/G/orders-test.yaml": "feature: true"})
	root := filepath.Join(s.dataDir, "workspace", "orders")
	os.MkdirAll(root, 0700)
	os.WriteFile(filepath.Join(root, "bootstrap.yml"), []byte(discoverySource("orders", up.URL)), 0600)
	os.WriteFile(filepath.Join(root, "application-test.yml"), []byte(discoveryDatabase("localDb", "local_test")), 0600)
	os.WriteFile(filepath.Join(root, "pom.xml"), []byte(discoveryProfiles()), 0600)
	got, err := s.discoverApplicationResources(context.Background(), "orders", resourceDiscoveryRequest{Environments: []string{"test"}, Connections: map[string]string{"test": "nacos-test"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range got.Items {
		if item.Kind == "mysql" {
			found = item.Database == "local_test" && item.Applyable && strings.Contains(item.Requirement.Sources["test"].File, "application-test.yml")
		}
	}
	if !found {
		t.Fatal("profile datasource or provenance lost", got)
	}
	sources, _, complete := resourceCustomDatasources(map[string]any{"common": map[string]any{"mysql": map[string]any{"jdbc-url": "jdbc:mysql://127.0.0.1:3306/shared_default?useSSL=false"}}, "jobs": map[string]any{"chosen": map[string]any{"mysql": map[string]any{"database": "chosen_test"}}, "other": map[string]any{"mysql": map[string]any{"database": "forbidden"}}}}, resourceDiscoveryRequest{Adapter: "flink", Job: "chosen"})
	if !complete || len(sources) != 1 || resourceString(resourceMap(sources["source_mysql"])["url"]) != "jdbc:mysql://127.0.0.1:3306/chosen_test?useSSL=false" {
		t.Fatal(sources, complete)
	}
}

func TestDiscoverySourcePatchPreservesOtherEnvironmentAndCAS(t *testing.T) {
	s, _, _ := discoveryRegressionFixture(t, nil)
	before := ApplicationResources{CompositionVersion: 2, OwnRequirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql", Sources: map[string]ResourceSourceEvidence{"dev": {State: "declared", File: "dev.yml"}, "test": {State: "pending_source"}}}}}
	if err := s.SaveApplicationResources("orders", before); err != nil {
		t.Fatal(err)
	}
	before = s.ApplicationResources("orders")
	patch := resourceBindingPatch{Revision: before.Revision, Environment: "test", Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql", Sources: map[string]ResourceSourceEvidence{"test": {State: "not_applicable", File: "test.yml"}}}}}
	value, err := s.prepareResourceBindingPatch("orders", patch, true)
	if err != nil {
		t.Fatal(err)
	}
	if value.OwnRequirements[0].Sources["dev"].File != "dev.yml" || s.ApplicationResources("orders").OwnRequirements[0].Sources["test"].State != "pending_source" {
		t.Fatal("preview mutated saved or other environment")
	}
	bad := patch
	bad.Requirements = []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql", Sources: map[string]ResourceSourceEvidence{"dev": {State: "not_applicable"}}}}
	if _, err = s.prepareResourceBindingPatch("orders", bad, true); err == nil {
		t.Fatal("cross-environment source update allowed")
	}
	if err = s.SaveApplicationResources("orders", value); err != nil {
		t.Fatal(err)
	}
	saved := s.ApplicationResources("orders")
	if saved.OwnRequirements[0].Sources["dev"].File != "dev.yml" || saved.OwnRequirements[0].Sources["test"].State != "not_applicable" {
		t.Fatal(saved)
	}
	if _, err = s.prepareResourceBindingPatch("orders", patch, true); err == nil {
		t.Fatal("stale patch accepted")
	}
	invalid := saved
	invalid.OwnRequirements[0].Sources["test"] = ResourceSourceEvidence{State: "arbitrary"}
	if err = s.SaveApplicationResources("orders", invalid); err == nil {
		t.Fatal("unknown source status accepted")
	}
}
