package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

type DocumentReportLinks struct {
	Revision  int64                 `json:"revision"`
	Items     []DocumentRequirement `json:"items"`
	Source    string                `json:"source,omitempty"`
	Actor     string                `json:"actor,omitempty"`
	UpdatedAt int64                 `json:"updated_at,omitempty"`
}

func documentReportLinksSchema(doc string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS api_document_report_links (report_id VARCHAR(64) PRIMARY KEY,revision BIGINT NOT NULL,document %s NOT NULL,source VARCHAR(32) NOT NULL,actor VARCHAR(191) NOT NULL,updated_at BIGINT NOT NULL)`, doc)
}

func scanReportLinks(row documentScanner) (DocumentReportLinks, error) {
	v := DocumentReportLinks{Items: []DocumentRequirement{}}
	var raw string
	e := row.Scan(&v.Revision, &raw, &v.Source, &v.Actor, &v.UpdatedAt)
	if e == sql.ErrNoRows {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(raw), &v.Items)
	return v, e
}

const reportLinksSelect = `SELECT revision,document,source,actor,updated_at FROM api_document_report_links WHERE report_id=?`

// Only the context explicitly submitted for THIS report supplies automatic links.
// A duplicate snapshot's older context must never be borrowed for a new report.
func recordAutomaticReportLinks(tx *deployTx, id string, context *DocumentChangeContext) error {
	if id == "" || context == nil {
		return nil
	}
	refs := map[int64]DocumentRequirement{}
	if context.WorkItem != nil {
		refs[context.WorkItem.ID] = *context.WorkItem
	}
	for _, ref := range context.Fields {
		refs[ref.ID] = ref
	}
	items := []DocumentRequirement{}
	for _, ref := range refs {
		if ref.ID > 0 {
			items = append(items, ref)
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	var actor string
	if e := tx.QueryRow(`SELECT actor FROM api_document_reports WHERE id=?`, id).Scan(&actor); e != nil {
		return e
	}
	raw, e := json.Marshal(items)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT OR IGNORE INTO api_document_report_links(report_id,revision,document,source,actor,updated_at) VALUES(?,1,?,'reported',?,?)`, id, string(raw), actor, time.Now().Unix())
	return e
}

func (s *Store) saveReportLinks(id, actor string, admin bool, revision int64, ids []int64) (DocumentReportLinks, error) {
	if revision < 0 || len(ids) > 500 {
		return DocumentReportLinks{}, fmt.Errorf("关联修订无效或超过500项")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return DocumentReportLinks{}, e
	}
	defer tx.Rollback()
	var owner string
	if e = tx.QueryRow(`SELECT actor FROM api_document_reports WHERE id=?`, id).Scan(&owner); e != nil {
		return DocumentReportLinks{}, fmt.Errorf("上报记录不存在")
	}
	if actor == "" || actor == "local" || (!admin && owner != actor) {
		return DocumentReportLinks{}, fmt.Errorf("仅上报人或管理员可维护关联")
	}
	if _, e = tx.Exec(`INSERT OR IGNORE INTO api_document_report_links(report_id,revision,document,source,actor,updated_at) VALUES(?,0,'[]','','',0)`, id); e != nil {
		return DocumentReportLinks{}, e
	}
	previous, e := scanReportLinks(tx.QueryRow(reportLinksSelect, id))
	if e != nil {
		return previous, e
	}
	if previous.Revision != revision {
		return previous, fmt.Errorf("关联已被修改，请保留选择并重新核对")
	}
	items := []DocumentRequirement{}
	seen := map[int64]bool{}
	for _, wid := range ids {
		if wid <= 0 || seen[wid] {
			return previous, fmt.Errorf("需求或Bug编号无效或重复")
		}
		seen[wid] = true
		work, err := scanWorkItem(tx.QueryRow(workItemSelect+` WHERE id=?`, wid))
		if err != nil {
			return previous, fmt.Errorf("需求或Bug #%d 不存在，请重新选择", wid)
		}
		if work.Kind != "bug" && work.Kind != "requirement" {
			return previous, fmt.Errorf("仅支持关联需求或Bug")
		}
		items = append(items, DocumentRequirement{ID: work.ID, Source: work.Source, ExternalID: work.ExternalID, Kind: work.Kind, Title: work.Title})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	raw, e := json.Marshal(items)
	if e != nil {
		return previous, e
	}
	now := time.Now().Unix()
	result, e := tx.Exec(`UPDATE api_document_report_links SET revision=revision+1,document=?,source='manual',actor=?,updated_at=? WHERE report_id=? AND revision=?`, string(raw), actor, now, id, revision)
	if e != nil {
		return previous, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return previous, e
	}
	if n != 1 {
		return previous, fmt.Errorf("关联已被修改，请保留选择并重新核对")
	}
	detail, _ := json.Marshal(map[string]any{"before": previous.Items, "after": items, "revision": revision + 1})
	if _, e = tx.Exec(`INSERT INTO audit_events(actor,action,resource_type,resource_id,detail,created_at) VALUES(?,?,?,?,?,?)`, actor, "link-work-items", "api-document-report", id, string(detail), now); e != nil {
		return previous, e
	}
	if e = tx.Commit(); e != nil {
		return previous, fmt.Errorf("关联保存结果待核对，请重新读取原记录，不要重复提交")
	}
	return DocumentReportLinks{Revision: revision + 1, Items: items, Source: "manual", Actor: actor, UpdatedAt: now}, nil
}

func registerDocumentReportLinkRoutes(mux *http.ServeMux, s *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("PUT /api/api-document-reports/{id}/work-items", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		report, e := s.documentReport(id)
		if e != nil {
			fail(w, 404, "上报记录不存在")
			return
		}
		actor := deliveryActor(r)
		admin := r.Header.Get("X-Role") == "admin"
		if actor == "" || actor == "local" || (!admin && report.Actor != actor) {
			fail(w, 403, "仅上报人或管理员可维护关联")
			return
		}
		var body struct {
			Revision *int64   `json:"revision"`
			IDs      *[]int64 `json:"work_item_ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil || body.Revision == nil || body.IDs == nil {
			fail(w, 400, "缺少关联修订或选择内容")
			return
		}
		links, e := s.saveReportLinks(id, actor, admin, *body.Revision, *body.IDs)
		if e != nil {
			fail(w, 409, e)
			return
		}
		wj(w, links)
	})
}
