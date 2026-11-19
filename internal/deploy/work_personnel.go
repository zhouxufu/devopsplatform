package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type researchPeopleSnapshot struct {
	endpoint string
	labels   map[string]string
	expires  time.Time
}

// Reuse display names already returned by an authorized live read. This cache
// is actor/source/endpoint scoped, bounded, and never imports users or grants access.
func (s *Store) rememberResearchPeople(actor string, c *ResourceConnection, people map[string]string) {
	if c == nil || len(people) == 0 {
		return
	}
	s.researchPeopleMu.Lock()
	defer s.researchPeopleMu.Unlock()
	if s.researchDisplayPeople == nil {
		s.researchDisplayPeople = map[zentaoSessionKey]researchPeopleSnapshot{}
	}
	for k, v := range s.researchDisplayPeople {
		if time.Now().After(v.expires) {
			delete(s.researchDisplayPeople, k)
		}
	}
	key := zentaoSessionKey{actor, c.Name}
	if _, ok := s.researchDisplayPeople[key]; !ok && len(s.researchDisplayPeople) >= 256 {
		return
	}
	labels := map[string]string{}
	for account, name := range people {
		if account != "" && name != "" && !strings.EqualFold(account, "closed") {
			labels[account] = name
		}
	}
	s.researchDisplayPeople[key] = researchPeopleSnapshot{c.Endpoint, labels, time.Now().Add(10 * time.Minute)}
}

func (s *Store) cachedResearchPeople(actor string) map[string]map[string]string {
	connections := s.Connections()
	s.researchPeopleMu.Lock()
	defer s.researchPeopleMu.Unlock()
	out := map[string]map[string]string{}
	for _, c := range connections {
		v, ok := s.researchDisplayPeople[zentaoSessionKey{actor, c.Name}]
		if !ok || v.endpoint != c.Endpoint || time.Now().After(v.expires) {
			continue
		}
		labels := map[string]string{}
		for account, name := range v.labels {
			labels[account] = name
		}
		out["zentao:"+c.Name] = labels
	}
	return out
}

// Only verified, actor-owned identities may expand a "mine" filter. Names are
// display metadata, never a substitute for an account or an authorization rule.
func (s *Store) workActorAccounts(actor string) map[string]string {
	out := map[string]string{"manual": actor}
	for _, c := range s.Connections() {
		if c.Kind != "zentao" {
			continue
		}
		account, status := s.researchAuthStatus(c.Name, actor)
		if account != "" && status != "needs_verification" {
			out["zentao:"+c.Name] = account
		}
	}
	return out
}

func workMineClause(prefix string, f WorkItemFilter) (string, []any) {
	if prefix != "" {
		prefix += "."
	}
	parts, args := []string{}, []any{}
	for source, account := range f.PersonAccounts {
		fields := []string{"assigned_to", "resolved_by", "opened_by", "closed_by"}
		if source == "manual" {
			if f.PersonRole == "" || f.PersonRole == "related" {
				fields = append(fields, "owner")
			} else {
				fields = []string{f.PersonRole}
			}
		} else if f.PersonRole == "owner" {
			continue
		} else if f.PersonRole != "" && f.PersonRole != "related" {
			fields = []string{f.PersonRole}
		}
		matches := []string{}
		args = append(args, source)
		for _, field := range fields {
			matches = append(matches, prefix+field+"=?")
			args = append(args, account)
		}
		parts = append(parts, "("+prefix+"source=? AND ("+strings.Join(matches, " OR ")+"))")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func validateManualAssignee(r *http.Request, item *WorkItem, current *WorkItem) error {
	if item.Source != "" && item.Source != "manual" || item.AssignedTo == "" || current != nil && item.AssignedTo == current.AssignedTo {
		return nil
	}
	// The gateway provides enabled platform accounts. Source accounts are never
	// inferred from display names or accepted as platform membership.
	var enabled []string
	if r.Header.Get("X-Reviewer-Directory") == "" && item.AssignedTo == deliveryActor(r) {
		return nil // Standalone mode still supports assigning to the current actor.
	}
	if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) == nil {
		for _, account := range enabled {
			if account == item.AssignedTo {
				return nil
			}
		}
	}
	return fmt.Errorf("负责人不可用，请重新选择启用的平台成员")
}

func registerWorkPersonnel(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/work-items/people-labels", func(w http.ResponseWriter, r *http.Request) {
		// Persisted snapshots plus this actor's recently read display metadata;
		// no implicit directory import or network request.
		rows, err := s.db.Query(`SELECT w.source,f.document FROM research_issue_fields f JOIN work_items w ON w.id=f.work_item_id`)
		if err != nil {
			deliveryWriteError(w, 500, "人员名称读取失败")
			return
		}
		defer rows.Close()
		out := map[string]map[string]string{}
		for rows.Next() {
			var source, raw string
			var fields ResearchIssueFields
			if rows.Scan(&source, &raw) != nil || json.Unmarshal([]byte(raw), &fields) != nil {
				deliveryWriteError(w, 500, "人员名称读取失败")
				return
			}
			if out[source] == nil {
				out[source] = map[string]string{}
			}
			for account, name := range fields.PersonLabels {
				if account != "" && !strings.EqualFold(account, "closed") {
					out[source][account] = name
				}
			}
		}
		if rows.Err() != nil {
			deliveryWriteError(w, 500, "人员名称读取失败")
			return
		}
		for source, labels := range s.cachedResearchPeople(deliveryActor(r)) {
			if out[source] == nil {
				out[source] = map[string]string{}
			}
			for account, name := range labels {
				out[source][account] = name
			}
		}
		deliveryJSON(w, out)
	})
}
