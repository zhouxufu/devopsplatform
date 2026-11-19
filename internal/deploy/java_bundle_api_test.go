package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJavaBundleAPIAtomicDefaultsApprovalAndSnapshot(t *testing.T) {
	s, m, _ := deliveryFixture(t)
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	post := func(method, path string, input any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		return w
	}
	sv := *s.GetService("orders")
	sv.ArtifactType = ArtifactJavaBundle
	sv.ArtifactGlob = "build/jpom-release"
	sv.BuildCmd = "bash deploy/jpom/build-release.sh"
	sv.Steps = []StepInst{{Step: "git-checkout"}, {Step: "java-build"}, {Step: javaBundleStep, Params: map[string]string{"ROOT": sv.ArtifactGlob, "MAIN_JAR": "lib/estate-gis-service.jar", "REQUIRED_PATHS": "bin/estate-gis-service.sh\ntools/changchun-traffic"}}}
	post("POST", "/api/services", sv, 200)
	for _, env := range []string{"test", "prod"} {
		state, e := s.applicationDeliveryDefault("orders", env)
		if e != nil {
			t.Fatal(e)
		}
		v := ServiceEnvironment{Service: "orders", Environment: env, TargetType: TargetJavaBundle, Hosts: []string{env + "-1"}, RemoteDir: "/isolated/gis", DeployConfig: map[string]string{"java_version": "21", "java_bundle_jar": "lib/estate-gis-service.jar", "java_port": "8080", "health_failure_action": "rollback"}, RuntimeEnvPatch: map[string]*string{"SPRING_PROFILES_ACTIVE": &env}}
		value := ApplicationDeliveryDefault{ArtifactSource: "existing", CodePolicy: CodePolicy{Mode: "integration", TargetBranch: "test-release"}}
		body := map[string]any{}
		raw, _ := json.Marshal(v)
		json.Unmarshal(raw, &body)
		body["delivery_default"] = map[string]any{"revision": state.Revision, "value": value}
		post("PUT", "/api/services/orders/environments/"+env, body, 200)
		got := s.GetServiceEnvironment("orders", env)
		if got.DeployConfig["java_mode"] != "managed" || got.HostGroup != "" || len(got.RuntimeEnv) != 0 || !strings.Contains(got.StartCmd, "current/service.sh") {
			t.Fatal("managed bundle scope lost", got)
		}
		v.RemoteDir = "/must-not-write"
		body["remote_dir"] = v.RemoteDir
		post("PUT", "/api/services/orders/environments/"+env, body, http.StatusBadRequest)
		if s.GetServiceEnvironment("orders", env).RemoteDir == v.RemoteDir {
			t.Fatal("stale default partially saved destination")
		}
		if env == "prod" {
			v.DeployConfig["health_failure_action"] = "keep"
			if s.UpsertServiceEnvironment(v) == nil {
				t.Fatal("protected bundle environment permits keep-running")
			}
		}
	}
	ws, _, p := gisBundleFixture(t)
	file, e := packageJavaBundle(context.Background(), ws, t.TempDir(), "orders", p, time.Now().Add(-time.Second))
	if e != nil {
		t.Fatal(e)
	}
	a, e := describeArtifact("orders", "fixture-gis", ArtifactJavaBundle, file, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterArtifact(&a); e != nil {
		t.Fatal(e)
	}
	if _, e = m.CreateArtifact("orders", a.ID, "prod", "", "alice"); e == nil {
		t.Fatal("protected bundle release accepted missing reason")
	}
	o, e := m.CreateArtifact("orders", a.ID, "prod", "isolated test only", "alice")
	if e != nil {
		t.Fatal(e)
	}
	if o.Target.TargetType != TargetJavaBundle || o.Target.DeployConfig["java_bundle_jar"] != p["MAIN_JAR"] || o.Status == "success" || o.Status == "running" {
		t.Fatal("approval or frozen target lost", o)
	}
	// Settings change after review must not mutate this frozen release snapshot.
	v := *s.GetServiceEnvironment("orders", "prod")
	v.DeployConfig["java_bundle_jar"] = "lib/new.jar"
	if e = s.UpsertServiceEnvironment(v); e != nil {
		t.Fatal(e)
	}
	if got := s.GetReleaseOrder(o.ID); got.Target.DeployConfig["java_bundle_jar"] != p["MAIN_JAR"] {
		t.Fatal("reviewed bundle identity mutated")
	}
}

func TestJavaBundlePipelineArchivesSyntheticBuildAndDownload(t *testing.T) {
	ws, root, p := gisBundleFixture(t)
	template := filepath.Join(ws, "fixture.jar")
	jar, _ := os.ReadFile(filepath.Join(root, p["MAIN_JAR"]))
	if e := os.WriteFile(template, jar, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sv := Service{Name: "gis", ArtifactType: ArtifactJavaBundle, BuildTool: "command", Workdir: ws, BuildCmd: "cp fixture.jar build/jpom-release/lib/estate-gis-service.jar", ArtifactGlob: p["ROOT"], Steps: []StepInst{{Step: "java-build"}, {Step: javaBundleStep, Params: p}}}
	if e = s.UpsertService(sv); e != nil {
		t.Fatal(e)
	}
	prepared, e := serviceForPipelineRun(&sv, PipelineRunOptions{})
	if e != nil {
		t.Fatal(e)
	}
	r := &PipelineRun{Service: "gis", Status: "running", Actor: "fixture", StartedAt: time.Now().Unix()}
	if e = s.CreateRun(r); e != nil {
		t.Fatal(e)
	}
	if e = s.saveBuildSnapshot(r.ID, prepared, s.GetServiceDelivery("gis")); e != nil {
		t.Fatal(e)
	}
	data := t.TempDir()
	builder := NewBuilder(s, data, NewLogHub())
	lib := NewStepLib("../../steps")
	if e = lib.ValidateBuildPipeline(prepared.Steps); e != nil {
		t.Fatal(e)
	}
	pr := NewPipelineRunner(s, builder, NewLogHub(), nil, lib)
	pr.run(r.ID, prepared, false)
	got := s.GetRun(r.ID)
	if got.Status != "success" || got.ArtifactKind != ArtifactJavaBundle {
		t.Fatal(got.Status, s.RunLog(r.ID))
	}
	a := s.GetArtifact(got.ArtifactID)
	if a == nil || a.Metadata["main_jar"] != p["MAIN_JAR"] {
		t.Fatal("bundle manifest metadata missing", a)
	}
	if _, e = inspectJavaBundle(a.URI, "gis", 21); e != nil {
		t.Fatal(e)
	}
	h := Routes(s, builder, NewLogHub(), "", lib)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/artifacts/"+strings.TrimSpace(javaBundleNumber(a.ID))+"/download", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), ".tar.gz") || w.Header().Get("X-Artifact-Digest") != a.Digest {
		t.Fatal(w.Code, w.Header())
	}
	copy := filepath.Join(t.TempDir(), "download.tar.gz")
	os.WriteFile(copy, w.Body.Bytes(), 0600)
	if _, e = inspectJavaBundle(copy, "gis", 21); e != nil {
		t.Fatal("download lost bundle", e)
	}
}

func javaBundleNumber(v int64) string { raw, _ := json.Marshal(v); return string(raw) }

func TestJavaBundleBuildProvenanceGatesBareHostBeforeSideEffects(t *testing.T) {
	s, _, _ := variantFixture(t, "gradle", "jar")
	sv := *s.GetService("orders")
	sv.ArtifactType = ArtifactJavaBundle
	sv.ArtifactGlob = "build/release"
	sv.Steps = nil
	if e := s.UpsertService(sv); e != nil {
		t.Fatal(e)
	}
	// The generic provenance gate applies to bundles too; a display label cannot
	// promote a test-compiled package into a configured production build contract.
	a := variantArtifact(t, s, "test")
	a.Kind = ArtifactJavaBundle
	if e := s.validateArtifactBuildVariant(a, "test"); e != nil {
		t.Fatal(e)
	}
	if e := s.validateArtifactBuildVariant(a, "prod"); e == nil {
		t.Fatal("test bundle accepted in production")
	}
	d := NewDeployer(s, NewLogHub(), "")
	sv.TargetType = TargetJavaBundle
	if _, e := d.StartArtifactRelease(&sv, Host{Name: "never-connect", Addr: "192.0.2.1"}, *a, "prod", "fixture", "alice"); e == nil {
		t.Fatal("bare host path bypassed build contract")
	}
	var n int
	if e := s.db.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&n); e != nil || n != 0 {
		t.Fatal("provenance rejection created remote execution record", n, e)
	}
}
