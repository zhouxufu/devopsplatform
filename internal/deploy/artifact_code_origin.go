package deploy

import "strings"

// A read-only presentation of the original preparation. Never use current
// application defaults to infer historical provenance or change checkout refs.
type artifactCodeOrigin struct {
	PreparationID  string   `json:"preparation_id"`
	Environment    string   `json:"environment"`
	TargetBranch   string   `json:"target_branch"`
	SourceBranches []string `json:"source_branches"`
	Commit         string   `json:"commit"`
}

func (s *Store) artifactCodeOrigin(a *Artifact) *artifactCodeOrigin {
	if a == nil || a.Source != "pipeline-run" {
		return nil
	}
	run := s.GetRun(a.SourceID)
	if run == nil || run.Service != a.Service {
		return nil
	}
	parts := strings.Split(run.Trigger, ":")
	if len(parts) != 3 || parts[0] != "code-preparation" || parts[2] != a.Service {
		return nil
	}
	p := s.CodePreparation(parts[1])
	if p == nil {
		return nil
	}
	for _, b := range p.Builds {
		if b.RunID != run.ID || b.Service != a.Service || b.Commit == "" || b.Commit != run.Branch {
			continue
		}
		for _, repo := range p.Repositories {
			member := repo.Service == a.Service
			for _, service := range repo.Services {
				member = member || service == a.Service
			}
			if !member || repo.ResultCommit != b.Commit || repo.Status != "merged" || repo.TargetBranch == "" {
				continue
			}
			origin := &artifactCodeOrigin{PreparationID: p.ID, Environment: p.Request.Environment, TargetBranch: repo.TargetBranch, Commit: b.Commit, SourceBranches: []string{}}
			seen := map[string]bool{}
			for _, source := range repo.Sources {
				if source.Branch != "" && !seen[source.Branch] {
					origin.SourceBranches = append(origin.SourceBranches, source.Branch)
					seen[source.Branch] = true
				}
			}
			return origin
		}
	}
	return nil
}
