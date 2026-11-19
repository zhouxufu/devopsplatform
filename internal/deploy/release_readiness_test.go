package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func readinessCheck(t *testing.T, r *ReleaseReadiness, key string) ReadinessCheck {
	t.Helper()
	for _, c := range r.Checks {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("missing readiness check %s", key)
	return ReadinessCheck{}
}
func TestReleaseReadinessManualAndMissingProofRemainUnverified(t *testing.T) {
	s, _, req := deliveryFixture(t)
	v := versionForBinding(t, s, &req)
	if _, err := s.SaveVersionArtifacts(v.ID, []VersionArtifactBinding{{Service: "orders", ArtifactID: req.Artifacts["orders"], CodeConfirmed: true}}, "alice"); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReleaseReadiness(v.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Code) != 1 || r.Code[0].Status != "manual" || r.Code[0].Commit != "" {
		t.Fatal("manual checkbox became proof", r.Code)
	}
	if readinessCheck(t, r, "target-orders").Status != "pass" || readinessCheck(t, r, "preflight").Status != "pending" || readinessCheck(t, r, "tests").Status != "pending" {
		t.Fatal("metadata became execution permission", r.Checks)
	}
	if _, err = s.db.Exec(`UPDATE artifacts SET digest='sha256:changed' WHERE id=?`, req.Artifacts["orders"]); err != nil {
		t.Fatal(err)
	}
	r, err = s.ReleaseReadiness(v.ID, "prod")
	if err != nil || r.Code[0].Status != "blocked" {
		t.Fatal("changed artifact accepted", err, r)
	}
}

func readinessProofFixture(t *testing.T) (*Store, *ReleaseVersion, *CodePreparation) {
	t.Helper()
	s, _, req := deliveryFixture(t)
	sv := *s.GetService("orders")
	sv.GitURL = "https://example.invalid/orders.git"
	if err := s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	v := versionForBinding(t, s, &req)
	sha := strings.Repeat("a", 40)
	build := &PipelineRun{Service: "orders", Actor: "alice", Branch: sha}
	if err := s.CreateRun(build); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Service: "orders", Version: sha[:7], Kind: "jar", URI: "/private-not-executed.jar", Digest: "sha256:evidence", Source: "pipeline-run", SourceID: build.ID}
	if err := s.RegisterArtifact(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE pipeline_runs SET artifact_id=? WHERE id=?`, a.ID, build.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO build_run_snapshots(run_id,config_cipher,commit_ref,created_at) VALUES(?,'private-snapshot',?,1)`, build.ID, sha); err != nil {
		t.Fatal(err)
	}
	s.FinishRun(build.ID, "success")
	if _, err := s.SaveVersionArtifacts(v.ID, []VersionArtifactBinding{{Service: "orders", ArtifactID: a.ID, CodeConfirmed: true}}, "alice"); err != nil {
		t.Fatal(err)
	}
	scope := &DeliveryScope{Type: "version", WorkItems: v.WorkItems, Changes: v.Changes}
	req.Artifacts = nil
	policy, err := s.deliveryPolicy("test")
	if err != nil {
		t.Fatal(err)
	}
	p := &CodePreparation{ID: "version-proof", Owner: "alice", State: "ready", Request: req, ScopeDigest: codeScopeDigest(scope), PolicyRevision: policy.Revision,
		Builds:       []PreparedBuild{{Service: "orders", Commit: sha, ConfigurationDigest: preparationConfiguration(s.GetService("orders"), s.GetServiceDelivery("orders")), RunID: build.ID, ArtifactID: a.ID, ArtifactDigest: a.Digest, Status: "success"}},
		Repositories: []PreparedRepository{{Service: "orders", URL: sv.GitURL, ConnectionDigest: preparationGitIdentity(sv.GitURL, nil), ResultCommit: sha, Sources: []PreparedSource{{Branch: v.Changes[0].Reference, Commit: strings.Repeat("b", 40), Changes: []int64{v.Changes[0].ID}}}}}}
	if err := s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	// Controlled metadata fixture: no source, Git, build runner or remote target is contacted.
	tpl := DeliveryTemplate{Name: "Unified evidence", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "code", Name: "Code", Kind: "code-prepare", ApplicationScope: "delivery", DefaultCodePolicy: &CodePolicy{Mode: "integration", TargetBranch: "test"}}}, Nodes: []DeliveryNode{{ID: "deploy", Name: "Deploy", Kind: "deploy"}}}
	if err := s.SaveDeliveryTemplate(&tpl); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.ResearchFlowPlan(DeliveryRequest{TemplateID: tpl.ID, VersionID: v.ID, SourceType: "version", Environment: "test"}, nil, nil, "alice")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := json.Marshal(prepared)
	steps, _ := json.Marshal([]DeliveryStep{{Kind: "code-prepare", Status: "success", Detail: p.ID}})
	res, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'fixture','success',?,'{}','alice','',1,1)`, string(plan), string(steps))
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := res.LastInsertId()
	if _, err = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, fmt.Sprintf("version-workflow-preparation:%d", v.ID), fmt.Sprint(runID)); err != nil {
		t.Fatal(err)
	}
	return s, s.GetReleaseVersion(v.ID), p
}

func TestReleaseReadinessCodeEvidenceInvalidatesOnChangedInputs(t *testing.T) {
	for _, mutation := range []string{"none", "commit", "status", "scope", "other-version", "repository", "configuration", "missing-source", "short-source", "mapping-scope"} {
		t.Run(mutation, func(t *testing.T) {
			s, v, p := readinessProofFixture(t)
			switch mutation {
			case "commit":
				s.db.Exec(`UPDATE build_run_snapshots SET commit_ref='' WHERE run_id=?`, p.Builds[0].RunID)
			case "status":
				s.FinishRun(p.Builds[0].RunID, "failed")
			case "scope":
				p.ScopeDigest = "stale"
			case "other-version":
				p.Request.VersionID = v.ID + 1
			case "repository":
				p.Repositories[0].ConnectionDigest = "stale"
			case "configuration":
				p.Builds[0].ConfigurationDigest = "stale"
			case "missing-source":
				p.Repositories[0].Sources = nil
			case "short-source":
				p.Repositories[0].Sources[0].Commit = "abcdef0"
			case "mapping-scope":
				var raw string
				s.db.QueryRow(`SELECT document FROM delivery_version_artifacts WHERE version_id=?`, v.ID).Scan(&raw)
				s.db.Exec(`UPDATE delivery_version_artifacts SET document=? WHERE version_id=?`, strings.Replace(raw, s.VersionArtifacts(v.ID).ScopeDigest, "stale", 1), v.ID)
			}
			if err := s.saveCodePreparation(p); err != nil {
				t.Fatal(err)
			}
			r, err := s.ReleaseReadiness(v.ID, "test")
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "none" {
				if r.Code[0].Status != "pass" || len(r.Code[0].Sources) != 1 || r.Code[0].PreparationID != p.ID {
					t.Fatal("valid recorded chain not proven", r.Code)
				}
			} else if r.Code[0].Status == "pass" {
				t.Fatal("stale/missing code evidence accepted", mutation, r.Code)
			}
			if readinessCheck(t, r, "exclusive-scope").Status != "manual" {
				t.Fatal("claimed no extra code")
			}
		})
	}
}

func TestReleaseReadinessAPIIsReadOnlyAndRedacted(t *testing.T) {
	s, v, _ := readinessProofFixture(t)
	var before, after int64
	s.db.QueryRow(`SELECT total_changes()`).Scan(&before)
	mux := http.NewServeMux()
	registerReleaseReadinessRoutes(mux, s)
	for _, tc := range []struct {
		path   string
		status int
	}{{fmt.Sprintf("/api/release-versions/%d/readiness?environment=test", v.ID), 200}, {fmt.Sprintf("/api/release-versions/%d/readiness?environment=missing", v.ID), 400}, {"/api/release-versions/0/readiness", 400}, {"/api/release-versions/bad/readiness", 400}, {"/api/release-versions/99999/readiness", 404}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"never-serialize-me", "private-snapshot", "private-not-executed", "example.invalid", "preflight_token", "connection_digest", "runtime_env"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("readiness leaked %s", secret)
			}
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("stale report can be cached")
		}
	}
	s.db.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after || len(s.ReleaseOrders("", 100)) != 0 {
		t.Fatal("readiness caused writes/execution", before, after)
	}
}

func TestReleaseReadinessDeduplicatesApplicationsAndDoesNotRequireCodeForConfig(t *testing.T) {
	s, _, req := deliveryFixture(t)
	v := versionForBinding(t, s, &req)
	changes := append([]Change{}, v.Changes...)
	extra := changes[0]
	extra.ID++
	v.Changes = append(changes, extra)
	if len(s.releaseCodeEvidence(v, VersionArtifacts{})) != 1 {
		t.Fatal("same app duplicated")
	}
	v.Changes = []Change{{ID: 123, Kind: "manual", Status: ChangeReady}}
	if len(s.releaseCodeEvidence(v, VersionArtifacts{})) != 0 {
		t.Fatal("configuration-only release requires a code artifact")
	}
}

func TestReleaseReadinessDraftDoesNotSilentlyOmitNewWorkChanges(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	w := &WorkItem{Source: "manual", Kind: "bug", Title: "Draft coverage", Owner: "alice"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "code", Service: "orders", Reference: "feature/one", Title: "First", Owner: "alice"}
	if err := s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	v := &ReleaseVersion{Version: "draft-scope", Title: "Draft", Owner: "alice"}
	if err := s.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{w.ID}}); err != nil {
		t.Fatal(err)
	}
	c2 := &Change{Kind: "code", Service: "orders", Reference: "feature/two", Title: "Later", Owner: "alice"}
	if err := s.CreateChange(c2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChangeWorkItems(c2.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReleaseReadiness(v.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if readinessCheck(t, r, fmt.Sprintf("new-changes-%d", w.ID)).Status != "blocked" {
		t.Fatal("draft omitted new change")
	}
}
