package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A read-only projection of current source fields, ownership and frozen runs.
// It neither enrolls a viewed Bug nor changes production eligibility.
type deliveryWorkFilter struct {
	WorkItemFilter
	Scope, Environment string
}
type deliveryWorkChange struct {
	ID, UpdatedAt  int64
	Status, Mode   string
	Origin, Target int64
	Proof          string
}
type deliveryWorkChild struct {
	ID         int64  `json:"id"`
	ExternalID string `json:"external_id"`
	Title      string `json:"title"`
}
type deliveryWorkRow struct {
	WorkItem
	Changes             []deliveryWorkChange `json:"-"`
	ChangeCount         int                  `json:"change_count"`
	ReadyCount          int                  `json:"ready_count"`
	Preparation         string               `json:"preparation"`
	Ownership           string               `json:"ownership"`
	Children            []deliveryWorkChild  `json:"linked_items"`
	DeliveryStatus      string               `json:"delivery_status"`
	RunID               int64                `json:"run_id,omitempty"`
	DeliveryEnvironment string               `json:"delivery_environment,omitempty"`
	SourceCheckedAt     int64                `json:"source_checked_at,omitempty"`
}
type deliveryWorkPage struct {
	Items        []deliveryWorkRow `json:"items"`
	Total        int               `json:"total"`
	Page         int               `json:"page"`
	PageSize     int               `json:"page_size"`
	Scope        string            `json:"scope"`
	CheckedAt    int64             `json:"checked_at"`
	SourceErrors []string          `json:"source_errors"`
}

func deliveryWorkProof(c Change) string {
	if len(c.Prerequisites) > 0 {
		return digestJSON([]any{c.Kind, c.Title, c.Service, c.Reference, c.Summary, c.RiskLevel, c.RollbackPlan, c.SQL, c.Nacos, c.Redis, c.Prerequisites})
	}
	return digestJSON([]any{c.Kind, c.Title, c.Service, c.Reference, c.Summary, c.RiskLevel, c.RollbackPlan, c.SQL, c.Nacos, c.Redis})
}

func (f *deliveryWorkFilter) validate(s *Store) error {
	if err := f.WorkItemFilter.Validate(); err != nil {
		return err
	}
	if f.Scope == "" {
		f.Scope = "production"
	}
	if f.Scope != "production" && f.Scope != "nonproduction" && f.Scope != "unassigned" {
		return fmt.Errorf("交付范围无效")
	}
	if f.Environment != "" {
		e := s.GetEnvironment(f.Environment)
		if e == nil || f.Scope != "nonproduction" || e.Purpose == "production" {
			return fmt.Errorf("请选择非生产用途的核对环境")
		}
	}
	return nil
}

// Batch only platform-associated IDs. The same native ACL and product guards
// as the personal pending queue apply. Never fall back to stored Bug status.
func (s *Store) deliveryWorkSources(ctx context.Context, actor string, local map[int64]WorkItem) (map[int64]WorkItem, []string) {
	fresh, groups := map[int64]WorkItem{}, map[string][]int64{}
	errors := []string{}
	for id, w := range local {
		if strings.HasPrefix(w.Source, "zentao:") {
			groups[strings.TrimPrefix(w.Source, "zentao:")] = append(groups[strings.TrimPrefix(w.Source, "zentao:")], id)
		} else if w.Source == "manual" {
			fresh[id] = w
		}
	}
	names := []string{}
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cachedLabels := s.cachedResearchPeople(actor)["zentao:"+name]
		z, c, identity, err := s.liveReadClient(ctx, name, actor, false)
		rows := map[int64]WorkItem{}
		ids := groups[name]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err == nil {
			for start := 0; start < len(ids); start += pendingBatchSize {
				batch := ids[start:min(start+pendingBatchSize, len(ids))]
				out := researchLivePage{Items: []researchLiveItem{}, People: map[string]string{}, Products: map[string]string{}, ReadIdentity: identity, Page: 1, PageSize: 100, CheckedAt: time.Now().Unix()}
				result, handled, e := s.livePendingRelease(ctx, z, c, researchLiveFilter{Connection: name, RegisteredIDs: batch, WorkItemFilter: WorkItemFilter{Page: 1, PageSize: 100}}, out, "")
				if e != nil {
					err = e
					break
				}
				if !handled {
					err = fmt.Errorf("来源不支持安全批量读取，请核对 apiGetModel 及来源权限配置")
					break
				}
				for _, v := range result.Items {
					people := map[string]string{}
					for account, label := range cachedLabels {
						people[account] = label
					}
					for account, label := range result.People {
						people[account] = label
					}
					v.PersonLabels = issuePersonLabels(people, v.AssignedTo, v.OpenedBy, v.ResolvedBy, v.ClosedBy)
					rows[v.ID] = v.WorkItem
				}
			}
		}
		if err != nil {
			errors = append(errors, name+"："+err.Error())
			continue
		}
		for id, w := range rows {
			fresh[id] = w
		}
	}
	return fresh, errors
}

func (s *Store) deliveryWorkInputs() (map[int64]WorkItem, map[int64][]deliveryWorkChange, error) {
	items, links := map[int64]WorkItem{}, map[int64][]deliveryWorkChange{}
	rows, err := s.db.Query(workItemSelect + ` WHERE EXISTS(SELECT 1 FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id WHERE wc.work_item_id=work_items.id) ORDER BY id LIMIT 5001`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		w, e := scanWorkItem(rows)
		if e != nil {
			rows.Close()
			return nil, nil, e
		}
		items[w.ID] = w
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(items) > 5000 {
		return nil, nil, fmt.Errorf("交付关联超过 5000 项，请先归档历史交付；未返回截断清单")
	}
	rows, err = s.db.Query(`SELECT wc.work_item_id,c.id,c.status,c.updated_at,COALESCE(co.mode,''),COALESCE(co.origin_work_item_id,0),COALESCE(co.delivery_work_item_id,0),c.kind,c.title,COALESCE(c.service,''),COALESCE(c.reference,''),COALESCE(c.summary,''),c.risk_level,COALESCE(c.rollback_plan,''),COALESCE(p.document,'{}') FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id LEFT JOIN change_delivery_ownership co ON co.change_id=c.id LEFT JOIN change_payloads p ON p.change_id=c.id WHERE c.status<>'archived' ORDER BY wc.work_item_id,c.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c deliveryWorkChange
		var change Change
		var payload string
		if err = rows.Scan(&id, &c.ID, &c.Status, &c.UpdatedAt, &c.Mode, &c.Origin, &c.Target, &change.Kind, &change.Title, &change.Service, &change.Reference, &change.Summary, &change.RiskLevel, &change.RollbackPlan, &payload); err != nil {
			return nil, nil, err
		}
		if err = json.Unmarshal([]byte(payload), &change); err != nil {
			return nil, nil, err
		}
		c.Proof = deliveryWorkProof(change)
		if _, ok := items[id]; ok {
			links[id] = append(links[id], c)
		}
	}
	return items, links, rows.Err()
}

func deliveryWorkMatches(w WorkItem, f WorkItemFilter, accounts map[string]string) bool {
	if f.Kind != "" && w.Kind != f.Kind {
		return false
	}
	if f.Status != "" {
		if f.Status == "open" {
			if w.Status == WorkItemClosed {
				return false
			}
		} else if f.Status == "closed" {
			if w.ExternalStatus != "closed" && w.Status != WorkItemClosed {
				return false
			}
		} else if w.ExternalStatus != f.Status {
			return false
		}
	}
	if f.Query != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(f.Query)) && !strings.Contains(w.ExternalID, f.Query) {
		return false
	}
	if f.Person == "" && len(accounts) == 0 {
		return true
	}
	person := f.Person
	if len(accounts) > 0 {
		person = accounts[w.Source]
		if w.Source == "manual" {
			person = accounts["manual"]
		}
		if person == "" {
			return false
		}
	}
	switch f.PersonRole {
	case "assigned_to":
		return w.AssignedTo == person
	case "resolved_by":
		return w.ResolvedBy == person
	case "opened_by":
		return w.OpenedBy == person
	case "closed_by":
		return w.ClosedBy == person
	case "owner":
		return w.Source == "manual" && w.Owner == person
	default:
		return w.AssignedTo == person || w.ResolvedBy == person || w.OpenedBy == person || w.ClosedBy == person || (w.Source == "manual" && w.Owner == person)
	}
}

func (s *Store) DeliveryWorkPage(ctx context.Context, actor string, admin bool, f deliveryWorkFilter) (deliveryWorkPage, error) {
	out := deliveryWorkPage{Items: []deliveryWorkRow{}, SourceErrors: []string{}, CheckedAt: time.Now().Unix()}
	if err := f.validate(s); err != nil {
		return out, err
	}
	out.Scope = f.Scope
	local, links, err := s.deliveryWorkInputs()
	if err != nil {
		return out, err
	}
	fresh, sourceErrors := s.deliveryWorkSources(ctx, actor, local)
	out.SourceErrors = sourceErrors
	ids := []int64{}
	for id := range fresh {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	all := []deliveryWorkRow{}
	for _, id := range ids {
		w := fresh[id]
		changes := []deliveryWorkChange{}
		children := map[int64]bool{}
		pending := false
		for _, c := range links[id] {
			if c.Mode == "follow" && c.Target != id {
				continue
			} // only render once, under its main item
			if f.Scope == "unassigned" {
				if c.Mode != "draft" && w.ReleaseRequired != nil {
					continue
				}
			} else if f.Scope == "production" {
				if c.Mode == "draft" {
					pending = true
				}
				if w.ReleaseRequired == nil || !*w.ReleaseRequired || c.Mode == "draft" {
					continue
				}
			}
			// Source visibility applies to inherited changes too; a hidden main or
			// child is never revealed by a cached association/title.
			if c.Mode == "follow" {
				if _, ok := fresh[c.Origin]; !ok {
					pending = true
					continue
				}
				children[c.Origin] = true
			}
			changes = append(changes, c)
		}
		if len(changes) == 0 {
			continue
		}
		matched := deliveryWorkMatches(w, f.WorkItemFilter, f.PersonAccounts)
		childRows := []deliveryWorkChild{}
		for child := range children {
			cw := fresh[child]
			childRows = append(childRows, deliveryWorkChild{ID: cw.ID, ExternalID: cw.ExternalID, Title: cw.Title})
			matched = matched || deliveryWorkMatches(cw, f.WorkItemFilter, f.PersonAccounts)
		}
		if !matched {
			continue
		}
		sort.Slice(childRows, func(i, j int) bool { return childRows[i].ID < childRows[j].ID })
		row := deliveryWorkRow{WorkItem: w, Changes: changes, Children: childRows, ChangeCount: len(changes), Ownership: "self", DeliveryStatus: "not_started", SourceCheckedAt: out.CheckedAt}
		if f.Scope == "unassigned" {
			row.Ownership = "unassigned"
		} else if len(children) > 0 {
			row.Ownership = "aggregated"
		}
		for _, c := range changes {
			if c.Status == ChangeReady {
				row.ReadyCount++
			}
		}
		row.Preparation = "draft"
		if row.ReadyCount == row.ChangeCount {
			row.Preparation = "ready"
		} else if row.ReadyCount > 0 {
			row.Preparation = "partial"
		}
		if pending {
			row.Preparation = "blocked"
		}
		all = append(all, row)
	}
	out.Total = len(all)
	out.Page = min(f.Page, max(1, (out.Total+f.PageSize-1)/f.PageSize))
	out.PageSize = f.PageSize
	start := min((out.Page-1)*f.PageSize, out.Total)
	out.Items = all[start:min(start+f.PageSize, out.Total)]
	for i := range out.Items {
		if err = s.deliveryWorkEvidence(&out.Items[i], f, actor, admin); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) deliveryWorkEvidence(row *deliveryWorkRow, f deliveryWorkFilter, actor string, admin bool) error {
	if f.Scope == "unassigned" {
		return nil
	}
	if row.Preparation == "blocked" {
		row.DeliveryStatus = "scope_unverified"
		return nil
	}
	// A bounded stream of only associated, immutable executions. No current
	// version status or Bug resolution is accepted as delivery evidence.
	before := int64(0)
	for {
		records, more, err := s.WorkDeliveryRecords(row.ID, before, actor, admin)
		if err != nil {
			return err
		}
		for _, record := range records {
			before = record.ID
			run := s.DeliveryRun(record.ID)
			if run == nil || run.Plan.Template.Workbench == "research" {
				continue
			}
			env := run.Plan.Environment
			purpose := env.Purpose
			if purpose == "" {
				if current := s.GetEnvironment(env.Name); current != nil {
					purpose = current.Purpose
				}
			}
			if f.Scope == "production" && purpose != "production" {
				continue
			}
			if f.Scope == "nonproduction" && (purpose == "production" || (f.Environment != "" && f.Environment != env.Name)) {
				continue
			}
			if f.Scope == "unassigned" {
				continue
			}
			frozen := []Change{}
			if run.Plan.Scope != nil {
				frozen = run.Plan.Scope.Changes
			} else if run.Plan.Version != nil {
				frozen = run.Plan.Version.Changes
			}
			covered := 0
			for _, current := range row.Changes {
				for _, c := range frozen {
					if c.ID == current.ID && deliveryWorkProof(c) == current.Proof && current.Status == ChangeReady {
						covered++
						break
					}
				}
			}
			if row.RunID == 0 {
				row.RunID = run.ID
				row.DeliveryEnvironment = env.DisplayName
				if row.DeliveryEnvironment == "" {
					row.DeliveryEnvironment = env.Name
				}
				row.DeliveryStatus = run.Status
				if run.Status == "success" {
					row.DeliveryStatus = "scope_changed"
					if covered > 0 {
						row.DeliveryStatus = "partial_success"
					}
				}
			}
			if run.Status == "success" && covered == row.ChangeCount {
				row.DeliveryStatus = "success"
				row.RunID = run.ID
				row.DeliveryEnvironment = env.DisplayName
				if row.DeliveryEnvironment == "" {
					row.DeliveryEnvironment = env.Name
				}
			}
			// Latest attempt wins; never hide a newer failure with older success.
			return nil
		}
		if !more || len(records) == 0 {
			return nil
		}
	}
}

func registerDeliveryWorkList(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/work-items/delivery-page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := deliveryWorkFilter{Scope: q.Get("scope"), Environment: q.Get("environment"), WorkItemFilter: WorkItemFilter{Query: q.Get("q"), Kind: q.Get("kind"), Status: q.Get("status"), Person: q.Get("person"), PersonRole: q.Get("person_role")}}
		for key, dest := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
			if q.Get(key) != "" {
				n, err := strconv.Atoi(q.Get(key))
				if err != nil || n <= 0 {
					deliveryWriteError(w, 400, "分页参数无效")
					return
				}
				*dest = n
			}
		}
		if q.Get("mine") == "1" {
			if deliveryActor(r) == "local" {
				deliveryWriteError(w, 403, "请先登录后使用本人筛选")
				return
			}
			f.PersonAccounts = s.workActorAccounts(deliveryActor(r))
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		out, err := s.DeliveryWorkPage(ctx, deliveryActor(r), r.Header.Get("X-Role") == "admin", f)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, out)
	})
}
