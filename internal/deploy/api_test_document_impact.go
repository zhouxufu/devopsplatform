package deploy

import (
	"fmt"
	"logplatform/pkg/apidoc"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type apiTestDocumentImpact struct {
	CaseID       int64                `json:"case_id"`
	CaseRevision int64                `json:"case_revision"`
	CaseName     string               `json:"case_name"`
	CanReview    bool                 `json:"can_review"`
	StepIndex    int                  `json:"step_index"`
	StepName     string               `json:"step_name"`
	OperationID  string               `json:"operation_id"`
	Environment  string               `json:"environment"`
	BaselineID   int64                `json:"baseline_id"`
	SnapshotID   int64                `json:"snapshot_id"`
	State        string               `json:"state"`
	Kind         string               `json:"kind"`
	Message      string               `json:"message"`
	Fields       []apidoc.FieldChange `json:"fields"`
	Truncated    bool                 `json:"truncated"`
	ReviewedBy   string               `json:"reviewed_by,omitempty"`
	ReviewedAt   int64                `json:"reviewed_at,omitempty"`
	Note         string               `json:"note,omitempty"`
	Inferred     bool                 `json:"inferred"`
}
type apiTestImpactCache struct {
	bytes       int
	documents   map[int64]APIDocumentSnapshot
	comparisons map[string]apidoc.Comparison
	latest      map[string]int64
}

func newAPITestImpactCache() *apiTestImpactCache {
	return &apiTestImpactCache{documents: map[int64]APIDocumentSnapshot{}, comparisons: map[string]apidoc.Comparison{}, latest: map[string]int64{}}
}
func (s *Store) impactDocument(app string, id int64, c *apiTestImpactCache) (APIDocumentSnapshot, error) {
	if v, ok := c.documents[id]; ok {
		return v, nil
	}
	v, e := s.apiDocumentSnapshot(app, id)
	if e == nil {
		if c.bytes+len(v.Document) > 32<<20 {
			return v, fmt.Errorf("关联文档较多，请进入具体用例分批核对（单次文档上限 32 MiB）")
		}
		c.bytes += len(v.Document)
		c.documents[id] = v
	}
	return v, e
}
func inferCaseOperation(request apiTestStep, doc APIDocumentSnapshot) string {
	_, ops, _, e := apidoc.Parse(doc.Document)
	if e != nil {
		return ""
	}
	path := request.Request.Path
	if u, e := url.Parse(path); e == nil {
		path = u.Path
	}
	matches := []string{}
	for _, op := range ops {
		if op.Method != request.Request.Method {
			continue
		}
		parts := strings.Split(op.Path, "/")
		for i, p := range parts {
			if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
				parts[i] = "[^/]+"
			} else {
				parts[i] = regexp.QuoteMeta(p)
			}
		}
		if matched, _ := regexp.MatchString("^"+strings.Join(parts, "/")+"$", path); matched {
			matches = append(matches, op.ID)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}
func (s *Store) apiTestDocumentImpacts(app string, c *apiTestCase, targetID int64, cache *apiTestImpactCache) ([]apiTestDocumentImpact, error) {
	out := []apiTestDocumentImpact{}
	for i, step := range c.Steps {
		baseID := step.DocumentID
		if baseID == 0 {
			baseID = c.DocumentID
		}
		if baseID == 0 {
			continue
		}
		row := apiTestDocumentImpact{CaseID: c.ID, CaseRevision: c.Revision, CaseName: c.Name, StepIndex: i, StepName: step.Name, BaselineID: baseID, State: "association_needed", Fields: []apidoc.FieldChange{}}
		base, e := s.impactDocument(app, baseID, cache)
		if e != nil {
			return nil, fmt.Errorf("关联文档不可读取")
		}
		row.Environment = base.Environment
		row.OperationID = step.OperationID
		if row.OperationID == "" {
			row.OperationID = inferCaseOperation(step, base)
			row.Inferred = row.OperationID != ""
		}
		selected := targetID
		if selected == 0 {
			var ok bool
			selected, ok = cache.latest[base.Environment+":"+base.Protocol]
			if !ok {
				if e = s.db.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, app, base.Environment, base.Protocol).Scan(&selected); e != nil {
					return nil, e
				}
				cache.latest[base.Environment+":"+base.Protocol] = selected
			}
		}
		target, e := s.impactDocument(app, selected, cache)
		if e != nil {
			return nil, fmt.Errorf("比较文档不可读取")
		}
		if target.Environment != base.Environment || target.Protocol != base.Protocol {
			continue
		}
		row.SnapshotID = selected
		if selected < baseID {
			row.Message = "目标快照早于用例关联版本，仅供历史查看"
			out = append(out, row)
			continue
		}
		if row.OperationID == "" {
			row.Message = "未能唯一识别关联接口，请在步骤中选择接口文档"
			out = append(out, row)
			continue
		}
		key := fmt.Sprint(baseID, ":", selected)
		diff, ok := cache.comparisons[key]
		if !ok {
			diff, e = apidoc.Compare(base.Document, target.Document)
			if e != nil {
				return nil, e
			}
			cache.comparisons[key] = diff
		}
		row.State = "current"
		row.Kind = "unchanged"
		row.Message = "关联接口定义未变化"
		row.Truncated = diff.Truncated
		for _, change := range diff.Changes {
			if change.ID == row.OperationID {
				row.Kind = change.Kind
				row.Fields = append([]apidoc.FieldChange(nil), change.Fields...)
				if len(row.Fields) > 20 {
					row.Fields = row.Fields[:20]
					row.Truncated = true
				}
				for j := range row.Fields {
					f := &row.Fields[j]
					if len(f.Before) > 1024 {
						f.Before = f.Before[:1024] + "…"
						row.Truncated = true
					}
					if len(f.After) > 1024 {
						f.After = f.After[:1024] + "…"
						row.Truncated = true
					}
				}
				row.Truncated = row.Truncated || change.Truncated
				if change.Kind != "unchanged" {
					row.State = "needs_review"
					row.Message = "文档已变更，请核对请求和断言；不会自动覆盖用例"
				}
				break
			}
		}
		if diff.Truncated && row.State == "current" {
			row.State = "needs_review"
			row.Message = "文档比较被截断，无法证明关联接口未变化"
		}
		if row.State == "needs_review" {
			if s.db.QueryRow(`SELECT actor,reviewed_at,note FROM api_test_document_reviews WHERE application_id=? AND case_id=? AND case_revision=? AND step_index=? AND snapshot_id=?`, app, c.ID, c.Revision, i, selected).Scan(&row.ReviewedBy, &row.ReviewedAt, &row.Note) == nil {
				row.State = "reviewed"
				row.Message = "已人工复核此用例修订与文档快照，不代表执行通过"
			}
		}
		out = append(out, row)
	}
	return out, nil
}
func (s *Store) validateCaseDocumentLinks(app string, c *apiTestCase) error {
	if c.DocumentID < 0 {
		return fmt.Errorf("文档快照标识无效")
	}
	for i, step := range c.Steps {
		if step.DocumentID < 0 || len(step.OperationID) > 1024 {
			return fmt.Errorf("步骤关联标识无效")
		}
		if step.OperationID == "" {
			if step.DocumentID > 0 {
				if _, e := s.apiTestDocument(app, step.DocumentID); e != nil {
					return e
				}
			}
			continue
		}
		id := step.DocumentID
		if id == 0 {
			id = c.DocumentID
		}
		if id == 0 {
			return fmt.Errorf("步骤 %d 的关联接口缺少文档快照", i+1)
		}
		d, e := s.apiDocumentSnapshot(app, id)
		if e != nil {
			return fmt.Errorf("关联文档不属于应用")
		}
		_, ops, _, e := apidoc.Parse(d.Document)
		found := false
		for _, op := range ops {
			if op.ID == step.OperationID {
				found = true
			}
		}
		if e != nil || !found {
			return fmt.Errorf("步骤 %d 的关联接口不在所选快照中", i+1)
		}
	}
	return nil
}
func registerAPITestImpactRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, admin func(*http.Request) bool, scope func(http.ResponseWriter, *http.Request) *Service, decode func(http.ResponseWriter, *http.Request, any) bool) {
	mux.HandleFunc("GET /api/api-testing/document-impacts", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		target, err := strconv.ParseInt(r.URL.Query().Get("snapshot_id"), 10, 64)
		if raw := r.URL.Query().Get("snapshot_id"); raw != "" && (err != nil || target <= 0) {
			fail(w, 400, "文档快照标识无效")
			return
		}
		if target > 0 {
			if _, e := st.apiTestDocument(sv.ApplicationID, target); e != nil {
				fail(w, 400, e)
				return
			}
		}
		caseID, err := strconv.ParseInt(r.URL.Query().Get("case_id"), 10, 64)
		if raw := r.URL.Query().Get("case_id"); raw != "" && (err != nil || caseID <= 0) {
			fail(w, 400, "用例标识无效")
			return
		}
		query := `SELECT c.id FROM api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id WHERE c.application_id=? AND (c.owner=? OR a.visibility='team') AND c.deleted=0`
		args := []any{sv.ApplicationID, actor(r)}
		if caseID > 0 {
			query += ` AND c.id=?`
			args = append(args, caseID)
		}
		query += ` ORDER BY c.id DESC LIMIT 201`
		rows, e := st.db.Query(query, args...)
		if e != nil {
			fail(w, 500, e)
			return
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		if len(ids) > 200 {
			fail(w, 400, "用例较多，请进入具体用例查看文档影响")
			return
		}
		out := []apiTestDocumentImpact{}
		cache := newAPITestImpactCache()
		for _, id := range ids {
			if caseID > 0 && id != caseID {
				continue
			}
			c, e := st.apiTestCase(sv.ApplicationID, actor(r), id)
			if e != nil {
				fail(w, 500, e)
				return
			}
			impacts, e := st.apiTestDocumentImpacts(sv.ApplicationID, c, target, cache)
			if e != nil {
				fail(w, 400, e)
				return
			}
			for i := range impacts {
				impacts[i].CanReview = caseCanEdit(c, actor(r), admin(r)) && impacts[i].State == "needs_review"
			}
			out = append(out, impacts...)
		}
		wj(w, out)
	})
	mux.HandleFunc("POST /api/api-testing/cases/{id}/document-review", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		c, e := st.apiTestCase(sv.ApplicationID, actor(r), id)
		if e != nil || !caseCanEdit(c, actor(r), admin(r)) {
			fail(w, 403, "无权复核该用例")
			return
		}
		var in struct {
			Revision   int64  `json:"revision"`
			StepIndex  int    `json:"step_index"`
			SnapshotID int64  `json:"snapshot_id"`
			Note       string `json:"note"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Revision != c.Revision || in.SnapshotID <= 0 || strings.TrimSpace(in.Note) == "" || len(in.Note) > 2000 {
			fail(w, 409, "请刷新用例版本并填写复核说明")
			return
		}
		impacts, e := st.apiTestDocumentImpacts(sv.ApplicationID, c, 0, newAPITestImpactCache())
		if e != nil {
			fail(w, 400, e)
			return
		}
		valid := false
		for _, impact := range impacts {
			if impact.StepIndex == in.StepIndex && impact.SnapshotID == in.SnapshotID && impact.State == "needs_review" {
				valid = true
			}
		}
		if !valid {
			fail(w, 409, "文档已变化或此项无需复核，请刷新")
			return
		}
		tx, e := st.db.Begin()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer tx.Rollback()
		var rev int64
		if e = tx.QueryRow(`SELECT revision FROM api_test_cases WHERE id=? AND deleted=0`, id).Scan(&rev); e != nil || rev != in.Revision {
			fail(w, 409, "用例已变化")
			return
		}
		_, e = tx.Exec(`INSERT INTO api_test_document_reviews(application_id,case_id,case_revision,step_index,snapshot_id,actor,reviewed_at,note) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(application_id,case_id,case_revision,step_index,snapshot_id) DO UPDATE SET actor=excluded.actor,reviewed_at=excluded.reviewed_at,note=excluded.note`, sv.ApplicationID, id, in.Revision, in.StepIndex, in.SnapshotID, actor(r), time.Now().Unix(), in.Note)
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		st.Audit(actor(r), "review-document-impact", "api-test-case", fmt.Sprint(id), map[string]any{"revision": in.Revision, "step": in.StepIndex, "snapshot_id": in.SnapshotID})
		wj(w, map[string]bool{"ok": true})
	})
}
