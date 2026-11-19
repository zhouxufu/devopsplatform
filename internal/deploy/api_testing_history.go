package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (m *apiTestManager) indexRun(v *apiTestRun, app string) error {
	tx, e := m.st.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT OR IGNORE INTO api_test_run_index(run_id,application_id,owner,source,environment,case_id,suite_id,name) VALUES(?,?,?,?,?,?,?,?)`, v.ID, app, v.Owner, apiTestRunSource(v), v.Environment, v.CaseID, v.SuiteID, v.Name)
	if e != nil {
		return e
	}
	ids := map[int64]bool{}
	if v.CaseID > 0 {
		ids[v.CaseID] = true
	}
	for _, unit := range v.Units {
		ids[unit.CaseSnapshot.ID] = true
	}
	for id := range ids {
		if _, e = tx.Exec(`INSERT OR IGNORE INTO api_test_run_cases(run_id,case_id) VALUES(?,?)`, v.ID, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Index old encrypted reports without modifying them. Current status remains authoritative in runs.
func (m *apiTestManager) backfillRunIndex(app, owner string) error {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()

	for {
		rows, e := m.st.db.Query(`SELECT r.id,r.document FROM api_test_runs r LEFT JOIN api_test_run_index i ON i.run_id=r.id WHERE r.application_id=? AND r.owner=? AND i.run_id IS NULL ORDER BY r.id LIMIT 50`, app, owner)
		if e != nil {
			return e
		}
		type item struct {
			id  int64
			enc string
		}
		pending := []item{}
		for rows.Next() {
			var v item
			if e = rows.Scan(&v.id, &v.enc); e != nil {
				rows.Close()
				return e
			}
			pending = append(pending, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(pending) == 0 {
			break
		}
		for _, item := range pending {
			raw, e := m.st.decryptSecret(item.enc)
			if e != nil {
				return fmt.Errorf("历史报告索引失败，请保留原记录并核对加密配置")
			}
			var v apiTestRun
			if e = json.Unmarshal([]byte(raw), &v); e != nil {
				return e
			}
			v.ID = item.id
			v.Owner = owner
			if e = m.indexRun(&v, app); e != nil {
				return e
			}
		}
	}

	return nil
}
func (m *apiTestManager) history(w http.ResponseWriter, r *http.Request, app, owner string, wj func(http.ResponseWriter, any)) error {
	q := r.URL.Query()
	source := q.Get("source")
	if source != "" && source != "manual" && source != "case" {
		return fmt.Errorf("记录来源无效")
	}
	status := q.Get("status")
	if status != "" && !map[string]bool{"queued": true, "running": true, "success": true, "failed": true, "unknown": true, "canceled": true}[status] {
		return fmt.Errorf("记录状态无效")
	}
	if e := m.backfillRunIndex(app, owner); e != nil {
		return e
	}
	where := `r.application_id=? AND r.owner=?`
	args := []any{app, owner}
	for _, filter := range []struct{ key, col string }{{"source", "i.source"}, {"environment", "i.environment"}, {"status", "r.status"}} {
		if v := q.Get(filter.key); v != "" {
			where += " AND " + filter.col + "=?"
			args = append(args, v)
		}
	}
	for _, filter := range []struct{ key, col, op string }{{"suite_id", "i.suite_id", "="}, {"from", "r.created_at", ">="}, {"to", "r.created_at", "<="}} {
		if v := q.Get(filter.key); v != "" {
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil || n < 0 {
				return fmt.Errorf("历史筛选参数无效")
			}
			where += " AND " + filter.col + filter.op + "?"
			args = append(args, n)
		}
	}
	if v := q.Get("case_id"); v != "" {
		id, e := strconv.ParseInt(v, 10, 64)
		if e != nil || id < 1 {
			return fmt.Errorf("用例编号无效")
		}
		where += ` AND EXISTS (SELECT 1 FROM api_test_run_cases c WHERE c.run_id=r.id AND c.case_id=?)`
		args = append(args, id)
	}
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		if len(v) > 200 {
			return fmt.Errorf("搜索词过长")
		}
		where += ` AND i.name LIKE ?`
		args = append(args, "%"+v+"%")
	}
	paged := q.Has("page")
	page, size := 1, 100
	if paged {
		var e error
		page, e = strconv.Atoi(q.Get("page"))
		if e != nil || page < 1 || page > 1000000 {
			return fmt.Errorf("页码无效")
		}
		size = 15
		if q.Get("page_size") != "" {
			size, e = strconv.Atoi(q.Get("page_size"))
			if e != nil || size < 1 || size > 50 {
				return fmt.Errorf("每页需要 1–50 条")
			}
		}
	}
	base := ` FROM api_test_runs r JOIN api_test_run_index i ON i.run_id=r.id WHERE ` + where
	var total int64
	if e := m.st.db.QueryRow(`SELECT COUNT(*)`+base, args...).Scan(&total); e != nil {
		return e
	}
	selectArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, e := m.st.db.Query(`SELECT r.id`+base+` ORDER BY r.id DESC LIMIT ? OFFSET ?`, selectArgs...)
	if e != nil {
		return e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	out := []map[string]any{}
	for _, id := range ids {
		v, e := m.load(app, owner, id)
		if e != nil {
			return e
		}
		out = append(out, apiTestRunSummary(v))
	}
	if paged {
		wj(w, map[string]any{"items": out, "total": total, "page": page, "page_size": size})
	} else {
		wj(w, out)
	}
	return nil
}
