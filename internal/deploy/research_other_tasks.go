package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (s *Store) researchOtherTasks(actor, view string) ([]ResearchTask, error) {
	out := []ResearchTask{}
	rows, err := s.db.Query(`SELECT id,kind,document,status,revision FROM workbench_queries ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	tickets := []WorkbenchQueryTicket{}
	for rows.Next() {
		var id int64
		var kind, raw, status string
		var revision int64
		if err = rows.Scan(&id, &kind, &raw, &status, &revision); err != nil {
			rows.Close()
			return nil, err
		}
		var q WorkbenchQueryTicket
		if json.Unmarshal([]byte(raw), &q) != nil {
			rows.Close()
			return nil, fmt.Errorf("查询申请记录无效")
		}
		q.ID = id
		q.Status = status
		q.Kind = kind
		q.Revision = revision
		tickets = append(tickets, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, q := range tickets {
		kind, id := q.Kind, q.ID
		visible := view == "done" && q.Approver == actor
		if view != "done" && q.Status == "pending_approval" && actor != q.Owner {
			visible = len(q.Approvers) > 0
			for env, allow := range q.Approvers {
				current := s.GetEnvironment(env)
				if current == nil || !allowedApprover(allow, actor) || !allowedApprover(current.Approvers, actor) {
					visible = false
				}
			}
		}
		if visible {
			label := "数据库查询审批"
			if kind == "redis" {
				label = "Redis 查询审批"
			}
			out = append(out, ResearchTask{ID: fmt.Sprintf("query:%d", id), SourceKind: "query", RunID: id, Kind: "approval", Title: label, Owner: q.Owner, Environment: q.Query.Environment, Workbench: kind, At: q.CreatedAt, Digest: queryHash(q), Status: q.Status})
		}
	}
	// Legacy single-application releases remain actionable in their existing
	// workspace because their approve action can start deployment immediately.
	// Do not disguise that operation as a non-executing workflow approval.
	rows, err = s.db.Query(releaseSelect + ` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	orders := []ReleaseOrder{}
	for rows.Next() {
		v, e := scanRelease(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		orders = append(orders, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, o := range orders {
		env := s.GetEnvironment(o.Environment)
		visible := false
		kind, title := "approval", "应用发布审批"
		if view == "done" {
			visible = o.Approver == actor
		} else if o.Status == "pending_approval" && actor != o.Actor && env != nil {
			visible = allowedApprover(o.Target.Approvers, actor) && allowedApprover(env.Approvers, actor)
		} else if o.Status == "waiting_next_batch" && o.Actor == actor {
			visible = true
			kind, title = "manual", "发布批次观察确认"
		}
		if visible {
			out = append(out, ResearchTask{ID: fmt.Sprintf("release:%d", o.ID), SourceKind: "release", RunID: o.ID, RunNumber: o.Number, Kind: kind, Title: title, Owner: o.Actor, Service: o.Service, Environment: o.Environment, At: o.StartedAt, Status: o.Status})
		}
	}
	return out, nil
}

func registerResearchQueryTasks(mux *http.ServeMux, s *Store) {
	visible := func(r *http.Request, id int64) bool {
		for _, view := range []string{"todo", "done"} {
			tasks, err := s.researchOtherTasks(deliveryActor(r), view)
			if err != nil {
				return false
			}
			for _, task := range tasks {
				if task.SourceKind == "query" && task.RunID == id {
					return true
				}
			}
		}
		return false
	}
	mux.HandleFunc("GET /api/research/query-tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if !visible(r, id) {
			deliveryWriteError(w, 403, "查询申请不属于本人的审批范围")
			return
		}
		var kind string
		s.db.QueryRow(`SELECT kind FROM workbench_queries WHERE id=?`, id).Scan(&kind)
		v, err := s.queryTicket(id, kind)
		writeIntegrationJSON(w, v, err)
	})
	mux.HandleFunc("POST /api/research/query-tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if !visible(r, id) {
			deliveryWriteError(w, 403, "查询申请不属于本人的审批范围")
			return
		}
		var body struct {
			Digest  string `json:"digest"`
			Approve bool   `json:"approve"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			deliveryWriteError(w, 400, "请求格式无效")
			return
		}
		var kind string
		s.db.QueryRow(`SELECT kind FROM workbench_queries WHERE id=?`, id).Scan(&kind)
		v, err := s.queryTicket(id, kind)
		if err != nil || queryHash(*v) != body.Digest {
			deliveryWriteError(w, 409, "查询申请已变化，请重新读取")
			return
		}
		action := "reject"
		if body.Approve {
			action = "approve"
		}
		s.handleWorkbenchQueries(w, r, kind, fmt.Sprintf("query-requests/%d/%s", id, action), []byte(`{}`))
	})
}
