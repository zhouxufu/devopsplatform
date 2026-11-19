package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	policy "logplatform/pkg/releasepolicy"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPortablePolicyLiveSQLPreviewAndManualAgree(t *testing.T) {
	s, state := researchLiveFixture(t, 4)
	state.mu.Lock()
	for i, row := range state.rows {
		row["project"] = "75"
		row["resolution"] = "fixed2"
		if i == 1 {
			row["resolution"] = "other"
		}
		if i == 2 {
			delete(row, "resolution")
		}
		if i == 3 {
			row["project"] = "165"
		}
	}
	state.mu.Unlock()
	p := policy.Policy{Version: 1, Default: "not_independent", Rules: []policy.Rule{{Name: "项目与修复方案", Match: "all", Decision: "independent", Conditions: []policy.Condition{{Field: "project", Operator: "in", Values: []string{"75"}}, {Field: "resolution", Operator: "in", Values: []string{"fixed2"}}}}}}
	v := ResearchReleaseRules{Enabled: true, Provider: "builtin", Policy: &p, Default: p.Default}
	savePolicyRules(t, s, v)
	for id := 1; id <= 4; id++ {
		if _, err := s.AttachResearchBug(context.Background(), "member", "issues", fmt.Sprint(id)); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		for _, filter := range []struct {
			value string
			n     int
		}{{"yes", 1}, {"no", 2}, {"unknown", 1}} {
			local, err := s.WorkCandidates(WorkCandidateFilter{Source: "zentao:issues", WorkItemFilter: WorkItemFilter{ReleaseRequired: filter.value}})
			if err != nil || local.Total != filter.n {
				t.Fatal("SQL", filter, local, err)
			}
			live, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Product: 1, WorkItemFilter: WorkItemFilter{ReleaseRequired: filter.value}})
			if err != nil || len(live.Items) != filter.n {
				t.Fatal("live", filter, live, err)
			}
		}
	}
	check()
	mux := http.NewServeMux()
	registerResearchDeliveryPolicy(mux, s)
	body := map[string]any{"enabled": true, "provider": "builtin", "policy": p, "bug_id": "1"}
	before := state.businessWrites
	response := policyRequest(t, mux, "POST", "/api/research/connections/issues/release-rules/preview", "admin", "admin", body)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "independent") {
		t.Fatal(response.Code, response.Body.String())
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM research_release_rules`).Scan(&count)
	if count != 1 || state.businessWrites != before {
		t.Fatal("preview wrote state")
	}
	if response = policyRequest(t, mux, "POST", "/api/research/connections/issues/release-rules/preview", "member", "member", body); response.Code != 403 {
		t.Fatal("member preview allowed")
	}
	// Saved policy re-read and legacy conversion are portable documents.
	got := policyRequest(t, mux, "GET", "/api/research/connections/issues/release-rules", "admin", "admin", nil)
	var doc ResearchReleaseRules
	json.Unmarshal(got.Body.Bytes(), &doc)
	if doc.Policy == nil || doc.Revision != 1 {
		t.Fatal(got.Body.String())
	}
	// A manual override remains authoritative over both query paths.
	item := s.GetWorkItem(1)
	no := false
	if err := s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "随主项交付"}, "member"); err != nil {
		t.Fatal(err)
	}
	local, err := s.WorkCandidates(WorkCandidateFilter{Source: "zentao:issues", WorkItemFilter: WorkItemFilter{ReleaseRequired: "no"}})
	if err != nil || local.Total != 3 {
		t.Fatal(local, err)
	}
}

func TestMySQLPortablePolicy(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_RESEARCH_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL schema")
	}
	s, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := &WorkItem{Source: "zentao:policy-fixture", ExternalID: "1", Kind: "bug", Title: "Alpha exact", ExternalStatus: "active"}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{Version: 1, Default: "unknown", Rules: []policy.Rule{{Name: "MySQL exact", Match: "all", Decision: "independent", Conditions: []policy.Condition{{Field: "title", Operator: "contains", Values: []string{"Alpha"}}, {Field: "kind", Operator: "in", Values: []string{"bug"}}}}}}
	raw, _ := json.Marshal(ResearchReleaseRules{Enabled: true, Policy: &p})
	_, err = s.db.Exec(`INSERT INTO research_release_rules(connection_name,document,revision,actor,updated_at) VALUES('policy-fixture',?,1,'',0)`, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkCandidates(WorkCandidateFilter{Source: w.Source, WorkItemFilter: WorkItemFilter{ReleaseRequired: "yes"}})
	if err != nil || got.Total != 1 {
		t.Fatal(got, err)
	}
}
func TestPortablePolicySQLOperatorParity(t *testing.T) {
	s, _ := researchLiveFixture(t, 1)
	w := &WorkItem{Source: "zentao:issues", ExternalID: "900", Kind: "bug", Title: "Alpha exact", ExternalStatus: "active"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"in", "not_in", "contains"} {
		for _, match := range []string{"all", "any"} {
			p := policy.Policy{Version: 1, Default: "not_independent", Rules: []policy.Rule{{Name: "test", Match: match, Decision: "independent", Conditions: []policy.Condition{{Field: "title", Operator: op, Values: []string{"Alpha"}}, {Field: "project", Operator: "in", Values: []string{"75"}}}}}}
			raw, _ := json.Marshal(ResearchReleaseRules{Enabled: true, Default: p.Default, Policy: &p})
			s.db.Exec(`INSERT INTO research_release_rules(connection_name,document,revision,actor,updated_at) VALUES('issues',?,1,'',0) ON CONFLICT(connection_name) DO UPDATE SET document=excluded.document`, string(raw))
			expect := policy.Evaluate(p, researchPolicyValues(w))
			value := "unknown"
			if expect.Decision == "independent" {
				value = "yes"
			} else if expect.Decision == "not_independent" {
				value = "no"
			}
			got, err := s.WorkCandidates(WorkCandidateFilter{Source: "zentao:issues", WorkItemFilter: WorkItemFilter{ReleaseRequired: value}})
			if err != nil || got.Total != 1 {
				t.Fatal(op, match, expect, got, err)
			}
		}
	}
}
