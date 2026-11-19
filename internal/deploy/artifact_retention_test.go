package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func retentionStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, name := range []string{"app", "other"} {
		if err = s.UpsertService(Service{Name: name, ArtifactType: "jar"}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func retentionFile(t *testing.T, s *Store, service string, index int) Artifact {
	t.Helper()
	dir := filepath.Join(s.dataDir, "artifacts", service)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	uri := filepath.Join(dir, fmt.Sprintf("v%d.jar", index))
	if err := os.WriteFile(uri, []byte(fmt.Sprint(index)), 0600); err != nil {
		t.Fatal(err)
	}
	a := Artifact{Service: service, Version: fmt.Sprint(index), URI: uri, Kind: ArtifactJar, Digest: fmt.Sprintf("sha256:%d", index)}
	if err := s.RegisterArtifact(&a); err != nil {
		t.Fatal(err)
	}
	return a
}
func assertRetained(t *testing.T, s *Store, a Artifact, want bool) {
	t.Helper()
	_, err := os.Stat(a.URI)
	if (err == nil) != want {
		t.Fatalf("artifact %d file existence=%v want=%v", a.ID, err, want)
	}
	if (s.GetArtifact(a.ID) != nil) != want {
		t.Fatalf("artifact %d availability mismatch", a.ID)
	}
}
func TestArtifactRetentionOptInAndPerApplication(t *testing.T) {
	s := retentionStore(t)
	artifacts := []Artifact{}
	for i := 0; i < 5; i++ {
		artifacts = append(artifacts, retentionFile(t, s, "app", i))
	}
	other := retentionFile(t, s, "other", 1)
	if err := s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	for _, a := range artifacts {
		assertRetained(t, s, a, true)
	}
	if err := s.SaveArtifactRetention("app", 2); err != nil {
		t.Fatal(err)
	}
	before, err := s.ArtifactRetention("app")
	if err != nil || before.Candidates != 3 {
		t.Fatal(before, err)
	}
	if err = s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	for i, a := range artifacts {
		assertRetained(t, s, a, i >= 3)
	}
	assertRetained(t, s, other, true)
	if err = s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	after, err := s.ArtifactRetention("app")
	if err != nil || after.Cleaned != 3 || after.Total != 2 {
		t.Fatal(after, err)
	}
	page, err := s.BuildList("artifacts", BuildListFilter{Service: "app"})
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	var history int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE service='app'`).Scan(&history); err != nil || history != 5 {
		t.Fatal("immutable history removed", history, err)
	}
	if len(s.Artifacts("app", 100)) != 2 {
		t.Fatal("retired artifacts still selectable")
	}
}
func TestArtifactRetentionProtectsRollbackAndFrozenReferences(t *testing.T) {
	s := retentionStore(t)
	a := []Artifact{}
	for i := 0; i < 11; i++ {
		a = append(a, retentionFile(t, s, "app", i))
	}
	// Historical deployments stay protected because arbitrary historical release
	// rollback still resolves its predecessor. Pending/uncertain records too.
	for i, status := range []string{"success", "success", "running", "timed_out"} {
		if _, err := s.db.Exec(`INSERT INTO deploys(service,version,host,environment,artifact_id,artifact_uri,status) VALUES('app',? ,?,'test',?,?,?)`, a[i].Version, fmt.Sprint(i), a[i].ID, a[i].URI, status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO release_orders(service,artifact_id,version,environment,status) VALUES('app',?,'v','test','pending_approval')`, a[4].ID); err != nil {
		t.Fatal(err)
	}
	version, _ := json.Marshal(VersionArtifacts{Bindings: []VersionArtifactBinding{{Service: "app", ArtifactID: a[5].ID}}})
	if _, err := s.db.Exec(`INSERT INTO delivery_environment_artifacts(version_id,environment,document,used) VALUES(1,'prod',?,1)`, string(version)); err != nil {
		t.Fatal(err)
	}
	plan, _ := json.Marshal(DeliveryPlan{Services: []DeliveryService{{Service: "app", Artifact: a[6]}}})
	if _, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'frozen','approved','[]','{}','fixture','',1,1)`, string(plan)); err != nil {
		t.Fatal(err)
	}
	prep, _ := json.Marshal(CodePreparation{Builds: []PreparedBuild{{Service: "app", ArtifactID: a[7].ID}}})
	if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('code_preparation:fixture',?)`, string(prep)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO pipeline_runs(service,status,artifact_id) VALUES('app','waiting_deploy',?)`, a[8].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveArtifactRetention("app", 1); err != nil {
		t.Fatal(err)
	}
	preview, err := s.ArtifactRetention("app")
	if err != nil || preview.Protected != 9 || preview.Candidates != 1 {
		t.Fatal(preview, err)
	}
	if err = s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	for i, artifact := range a {
		assertRetained(t, s, artifact, i != 9)
	}
}
func TestArtifactRetentionUnmanagedSymlinksAndSharedFiles(t *testing.T) {
	s := retentionStore(t)
	external := filepath.Join(t.TempDir(), "external.jar")
	if err := os.WriteFile(external, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := retentionFile(t, s, "app", 0)
	copy := alias
	copy.ID = 0
	copy.Version = "alias"
	if err := s.RegisterArtifact(&copy); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.dataDir, "artifacts", "app")
	symlink := filepath.Join(dir, "symlink.jar")
	if err := os.Symlink(external, symlink); err != nil {
		t.Fatal(err)
	}
	for _, a := range []Artifact{{Service: "app", Version: "external", URI: external, Kind: ArtifactJar}, {Service: "app", Version: "link", URI: symlink, Kind: ArtifactJar}, {Service: "app", Version: "registry", URI: "registry.invalid/app@sha256:fake", Kind: ArtifactOCI}} {
		if err := s.RegisterArtifact(&a); err != nil {
			t.Fatal(err)
		}
	}
	newest := retentionFile(t, s, "app", 9)
	if err := s.SaveArtifactRetention("app", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	assertRetained(t, s, alias, true)
	assertRetained(t, s, copy, true)
	assertRetained(t, s, newest, true)
	if _, err := os.Stat(external); err != nil {
		t.Fatal("external file removed", err)
	}
	if _, err := os.Lstat(symlink); err != nil {
		t.Fatal("symlink removed", err)
	}
	policy, err := s.ArtifactRetention("app")
	if err != nil || policy.Candidates != 0 || policy.Unmanaged != 5 {
		t.Fatal(policy, err)
	}
}
func TestArtifactRetentionMalformedReferenceStopsCleanup(t *testing.T) {
	s := retentionStore(t)
	old := retentionFile(t, s, "app", 1)
	retentionFile(t, s, "app", 2)
	s.SaveArtifactRetention("app", 1)
	if _, err := s.db.Exec(`INSERT INTO delivery_version_artifacts(version_id,document,used) VALUES(1,'broken-json',1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyArtifactRetention("app"); err == nil {
		t.Fatal("malformed reference silently ignored")
	}
	assertRetained(t, s, old, true)
}
func TestArtifactRetentionCrashRecoveryAndStaleReferences(t *testing.T) {
	s := retentionStore(t)
	old := retentionFile(t, s, "app", 1)
	retentionFile(t, s, "app", 2)
	if _, err := s.db.Exec(`INSERT INTO artifact_cleanup(artifact_id,service,uri,retired_at,cleaned_at) VALUES(?,'app',?,1,0)`, old.ID, old.URI); err != nil {
		t.Fatal(err)
	}
	// Retired bytes can still exist after a crash, but no new deployment can pin
	// the obsolete record between retirement and file deletion.
	if err := s.CreateDeploy(&Deploy{Service: "app", Host: "fake", ArtifactID: old.ID}); err == nil {
		t.Fatal("retired artifact accepted by deploy")
	}
	if err := s.CreateReleaseOrder(&ReleaseOrder{Service: "app", ArtifactID: old.ID, Status: "failed"}); err == nil {
		t.Fatal("retired artifact accepted by release")
	}
	if err := s.RegisterArtifact(&old); err == nil {
		t.Fatal("retired artifact resurrected")
	}
	if err := s.SaveArtifactRetention("app", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	assertRetained(t, s, old, false)
	policy, err := s.ArtifactRetention("app")
	if err != nil || policy.Pending != 0 || policy.Cleaned != 1 {
		t.Fatal(policy, err)
	}
}
func TestArtifactRetentionConcurrentPinOrCleanup(t *testing.T) {
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := retentionStore(t)
			old := retentionFile(t, s, "app", 1)
			retentionFile(t, s, "app", 2)
			s.SaveArtifactRetention("app", 1)
			var wg sync.WaitGroup
			var pinErr, cleanErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				pinErr = s.CreateDeploy(&Deploy{Service: "app", Host: "fake", ArtifactID: old.ID, ArtifactURI: old.URI})
			}()
			go func() { defer wg.Done(); cleanErr = s.ApplyArtifactRetention("app") }()
			wg.Wait()
			if cleanErr != nil {
				t.Fatal(cleanErr)
			}
			if pinErr == nil {
				assertRetained(t, s, old, true)
			} else {
				assertRetained(t, s, old, false)
			}
		})
	}
}
func TestArtifactRetentionAPIPermissionsAndValidation(t *testing.T) {
	s := retentionStore(t)
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(t.TempDir()))
	for _, spec := range []struct {
		role, body string
		status     int
	}{{"member", `{"keep_count":2}`, 403}, {"admin", `{"keep_count":-1}`, 400}, {"admin", `{"keep_count":1.2}`, 400}, {"admin", `{}`, 400}, {"admin", `{"keep_count":10001}`, 400}, {"admin", `{"keep_count":2}`, 200}, {"admin", `{"keep_count":0}`, 200}} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/services/app/artifact-retention", strings.NewReader(spec.body))
		req.Header.Set("X-User", "alice")
		req.Header.Set("X-Role", spec.role)
		h.ServeHTTP(rec, req)
		if rec.Code != spec.status {
			t.Fatal(spec, rec.Code, rec.Body.String())
		}
	}
	if v, err := s.ArtifactRetention("app"); err != nil || v.KeepCount != 0 {
		t.Fatal(v, err)
	}
}

func TestArtifactRetentionProtectsBuildReferencedBeforeArtifactBinding(t *testing.T) {
	s := retentionStore(t)
	old := retentionFile(t, s, "app", 1)
	retentionFile(t, s, "app", 2)
	run := &PipelineRun{Service: "app", Actor: "fixture"}
	if err := s.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE pipeline_runs SET artifact_id=?,status='success' WHERE id=?`, old.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	prep, _ := json.Marshal(CodePreparation{Builds: []PreparedBuild{{Service: "app", RunID: run.ID}}})
	if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('code_preparation:awaiting-artifact',?)`, string(prep)); err != nil {
		t.Fatal(err)
	}
	s.SaveArtifactRetention("app", 1)
	if err := s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	assertRetained(t, s, old, true)
}
func TestArtifactRetentionCanonicalAliasesAndRetirementCannotResurrect(t *testing.T) {
	s := retentionStore(t)
	old := retentionFile(t, s, "app", 1)
	parent := filepath.Join(filepath.Dir(old.URI), "nested")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	alias := old
	alias.ID = 0
	alias.Version = "alias"
	alias.URI = parent + "/../v1.jar"
	if err := s.RegisterArtifact(&alias); err != nil {
		t.Fatal(err)
	}
	retentionFile(t, s, "app", 2)
	s.SaveArtifactRetention("app", 1)
	if err := s.ApplyArtifactRetention("app"); err != nil {
		t.Fatal(err)
	}
	assertRetained(t, s, old, true)
	assertRetained(t, s, alias, true)
	// Simulate a committed retirement marker; alternative spellings of that
	// path must not create a new live alias while the cleanup worker runs.
	if _, err := s.db.Exec(`INSERT INTO artifact_cleanup(artifact_id,service,uri,retired_at,cleaned_at) VALUES(?,'app',?,1,0)`, old.ID, old.URI); err != nil {
		t.Fatal(err)
	}
	alias.Source = "manual"
	alias.SourceID = 42
	if err := s.RegisterArtifact(&alias); err == nil {
		t.Fatal("alternative path spelling resurrected retired bytes")
	}
}
