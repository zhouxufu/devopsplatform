package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArtifactCodeOriginUsesLinkedFrozenPreparation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	commit := strings.Repeat("a", 40)
	result, err := s.db.Exec(`INSERT INTO pipeline_runs(service,status,branch,version,trigger,started_at) VALUES(?,?,?,?,?,?)`, "orders", "success", commit, commit[:11], "code-preparation:frozen:orders", 1)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := result.LastInsertId()
	p := &CodePreparation{ID: "frozen", Owner: "alice", Request: DeliveryRequest{Environment: "dev"}, Builds: []PreparedBuild{{Service: "orders", RunID: runID, Commit: commit}}, Repositories: []PreparedRepository{{Service: "other-module", Services: []string{"orders"}, Status: "merged", TargetBranch: "shared-dev", ResultCommit: commit, Sources: []PreparedSource{{Branch: "feature/work"}, {Branch: "feature/work"}}}}}
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	artifact := Artifact{Service: "orders", Source: "pipeline-run", SourceID: runID, Metadata: map[string]string{"branch": commit}, Version: commit[:11], Kind: "jar", URI: "/fixture.jar", Digest: "sha256:fixture"}
	if err = s.RegisterArtifact(&artifact); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(s.GetArtifact(artifact.ID))
	prepBefore := s.CodePreparation(p.ID).raw
	// A changed current default must not relabel the original build.
	if err = s.UpsertService(Service{Name: "orders", Branch: "different-current-branch", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	origin := s.artifactCodeOrigin(&artifact)
	if origin == nil || origin.TargetBranch != "shared-dev" || origin.Commit != commit || origin.Environment != "dev" || len(origin.SourceBranches) != 1 || origin.SourceBranches[0] != "feature/work" {
		t.Fatalf("unexpected origin: %+v", origin)
	}
	page, err := s.BuildList("artifacts", BuildListFilter{Service: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if !strings.Contains(string(raw), `"target_branch":"shared-dev"`) {
		t.Fatalf("missing origin: %s", raw)
	}
	after, _ := json.Marshal(s.GetArtifact(artifact.ID))
	if string(before) != string(after) || s.CodePreparation(p.ID).raw != prepBefore {
		t.Fatal("read changed immutable records")
	}
	for _, tc := range []struct {
		name   string
		change func(*Artifact)
	}{
		{"wrong service", func(a *Artifact) { a.Service = "foreign" }},
		{"wrong run", func(a *Artifact) { a.SourceID = runID + 1 }},
		{"unmanaged artifact", func(a *Artifact) { a.Source = "manual" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := artifact
			tc.change(&a)
			if s.artifactCodeOrigin(&a) != nil {
				t.Fatal("unrelated provenance accepted")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*CodePreparation)
	}{
		{"commit mismatch", func(p *CodePreparation) { p.Builds[0].Commit = strings.Repeat("b", 40) }},
		{"run mismatch", func(p *CodePreparation) { p.Builds[0].RunID++ }},
		{"repository mismatch", func(p *CodePreparation) { p.Repositories[0].Services = []string{"foreign"} }},
		{"result mismatch", func(p *CodePreparation) { p.Repositories[0].ResultCommit = strings.Repeat("b", 40) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var altered CodePreparation
			if err := json.Unmarshal([]byte(prepBefore), &altered); err != nil {
				t.Fatal(err)
			}
			altered.raw = s.CodePreparation(p.ID).raw
			tc.change(&altered)
			if err := s.saveCodePreparation(&altered); err != nil {
				t.Fatal(err)
			}
			if s.artifactCodeOrigin(&artifact) != nil {
				t.Fatal("unverified linkage accepted")
			}
		})
	}
}
