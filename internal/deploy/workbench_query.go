package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Defaults retain the existing write-review requirement. Policies are scoped to
// one plugin/environment; a protected environment always requires write review.
type WorkbenchPolicy struct {
	QueryApproval  bool `json:"query_approval"`
	ChangeApproval bool `json:"change_approval"`
}

func (s *Store) workbenchPolicy(kind, environment string) (WorkbenchPolicy, error) {
	p := WorkbenchPolicy{ChangeApproval: true}
	var data string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "workbench_policy:"+kind+":"+environment).Scan(&data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, nil
		}
		return p, fmt.Errorf("读取审批设置失败")
	}
	if json.Unmarshal([]byte(data), &p) != nil {
		return p, fmt.Errorf("审批设置损坏，请联系管理员")
	}
	return p, nil
}
func deliveryReviewRequired(p *DeliveryPlan) bool {
	if p.Environment.Protected {
		return true
	}
	if p.Template.Workbench != "" {
		return p.WorkbenchPolicy == nil || p.WorkbenchPolicy.ChangeApproval
	}
	for _, node := range p.Nodes {
		if node.Kind == "mysql" || node.Kind == "redis" {
			return true
		}
	}
	return len(p.RedisPlans) > 0 || len(p.SQLPlans) > 0
}

type WorkbenchQuery struct {
	Service     string        `json:"service"`
	Environment string        `json:"environment"`
	Alias       string        `json:"resource_alias"`
	SQL         string        `json:"sql,omitempty"`
	Commands    string        `json:"commands,omitempty"`
	TaskID      int64         `json:"task_id,omitempty"`
	Scope       DataSyncScope `json:"scope,omitempty"`
}
type WorkbenchQueryTicket struct {
	ID           int64             `json:"id"`
	Kind         string            `json:"kind"`
	Query        WorkbenchQuery    `json:"query"`
	Status       string            `json:"status"`
	Owner        string            `json:"owner"`
	Approver     string            `json:"approver"`
	Approvers    map[string]string `json:"approvers"`
	TargetDigest string            `json:"target_digest"`
	ResultDigest string            `json:"result_digest,omitempty"`
	CreatedAt    int64             `json:"created_at"`
	Revision     int64             `json:"revision"`
}

func queryHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) queryTargets(kind string, q WorkbenchQuery) (map[string]string, string, error) {
	approvers := map[string]string{}
	connections := []*ResourceConnection{}
	targets := []string{q.Environment}
	var task *DataSyncTask
	if q.TaskID != 0 {
		if kind != "dbms" {
			return nil, "", fmt.Errorf("Redis 不支持数据对比")
		}
		var err error
		task, err = s.DataSyncTask(q.TaskID)
		if err != nil || task == nil {
			return nil, "", fmt.Errorf("数据同步任务不存在")
		}
		q.Service = task.Service
		q.Alias = task.ResourceAlias
		targets = []string{q.Scope.SourceEnvironment, q.Scope.TargetEnvironment}
		if targets[0] == targets[1] {
			return nil, "", fmt.Errorf("来源与目标环境不能相同")
		}
	} else if kind == "dbms" {
		if _, err := parseWorkbenchSQL(q.SQL); err != nil {
			return nil, "", err
		}
	} else {
		if _, err := parseRedisCommandText(q.Commands, redisReadKeys); err != nil {
			return nil, "", err
		}
	}
	for _, name := range targets {
		env := s.GetEnvironment(name)
		if env == nil {
			return nil, "", fmt.Errorf("请选择有效环境")
		}
		approvers[name] = env.Approvers
		var c *ResourceConnection
		var err error
		if kind == "dbms" {
			c, err = s.sqlBinding(q.Service, name, q.Alias)
		} else {
			c, err = s.redisBinding(q.Service, name, q.Alias)
		}
		if err != nil {
			return nil, "", err
		}
		if kind == "redis" && q.TaskID == 0 {
			commands, e := parseRedisCommandText(q.Commands, redisReadKeys)
			if e != nil {
				return nil, "", e
			}
			if e = validateRedisReadScope(commands, c); e != nil {
				return nil, "", e
			}
		}
		connections = append(connections, c)
	}
	// Includes credentials and binding overrides only in a hash, never in tickets.
	return approvers, queryHash([]any{connections, task}), nil
}
func (s *Store) queryNeedsReview(kind string, q WorkbenchQuery) (bool, error) {
	targets := []string{q.Environment}
	if q.TaskID != 0 {
		targets = []string{q.Scope.SourceEnvironment, q.Scope.TargetEnvironment}
	}
	required := false
	for _, env := range targets {
		p, err := s.workbenchPolicy(kind, env)
		if err != nil {
			return false, err
		}
		required = required || p.QueryApproval
	}
	return required, nil
}
func (s *Store) executeWorkbenchQuery(ctx context.Context, kind string, q WorkbenchQuery, actor string) (any, error) {
	if q.TaskID != 0 {
		t, err := s.DataSyncTask(q.TaskID)
		if err != nil {
			return nil, err
		}
		return s.CompareDataSync(ctx, t, q.Scope)
	}
	if kind == "dbms" {
		parsed, err := parseWorkbenchSQL(q.SQL)
		if err != nil {
			return nil, err
		}
		c, err := s.sqlBinding(q.Service, q.Environment, q.Alias)
		if err != nil {
			return nil, err
		}
		result, err := readWorkbenchSQL(ctx, c, parsed)
		if err != nil {
			return nil, err
		}
		s.Audit(actor, "query", "mysql", c.Name, map[string]any{"environment": q.Environment, "service": q.Service, "table": parsed.Table, "rows": len(result["rows"].([]map[string]any))})
		return result, nil
	}
	commands, err := parseRedisCommandText(q.Commands, redisReadKeys)
	if err != nil {
		return nil, err
	}
	c, err := s.redisBinding(q.Service, q.Environment, q.Alias)
	if err != nil {
		return nil, err
	}
	if err = validateRedisReadScope(commands, c); err != nil {
		return nil, err
	}
	started := time.Now()
	client, err := newRedisClient(ctx, c, true)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	rows := []map[string]any{}
	totalBytes := 0
	for _, a := range commands {
		args := make([]any, len(a))
		for i, arg := range a {
			args[i] = arg
		}
		var result any
		truncated := false
		if a[0] == "GET" {
			// Avoid allocating an unbounded string reply; GETRANGE is read-only.
			typ, e := client.Type(ctx, a[1]).Result()
			if e != nil {
				return nil, fmt.Errorf("Redis 查询失败：%s", redisErrorCategory(e))
			}
			if typ == "none" {
				result = nil
			} else if typ != "string" {
				return nil, fmt.Errorf("GET 仅支持字符串 Key")
			} else {
				text, e := client.GetRange(ctx, a[1], 0, 65536).Result()
				if e != nil {
					return nil, fmt.Errorf("Redis 查询失败：%s", redisErrorCategory(e))
				}
				truncated = len(text) > 65536
				if truncated {
					text = text[:65536]
				}
				result = text
			}
		} else {
			result, err = client.Do(ctx, args...).Result()
			if err == redis.Nil {
				result = nil
				err = nil
			}
			if err != nil {
				return nil, fmt.Errorf("Redis 查询失败：%s", redisErrorCategory(err))
			}
		}
		encoded, _ := json.Marshal(result)
		totalBytes += len(encoded)
		if totalBytes > 2<<20 {
			return nil, fmt.Errorf("查询结果超过 2 MiB，请减少命令或缩小范围")
		}
		rows = append(rows, map[string]any{"command": strings.Join(a, " "), "argv": a, "result": result, "truncated": truncated})
	}
	s.Audit(actor, "query", "redis", c.Name, map[string]any{"environment": q.Environment, "service": q.Service, "commands": len(commands)})
	return map[string]any{"columns": []string{"command", "result", "truncated"}, "rows": rows, "elapsed_ms": time.Since(started).Milliseconds(), "target": map[string]any{"connection": c.Name, "db": c.Config["db"], "key_prefix": c.Config["key_prefix"]}}, nil
}
func (s *Store) queryTicket(id int64, kind string) (*WorkbenchQueryTicket, error) {
	var data, status string
	var rev int64
	if s.db.QueryRow(`SELECT document,status,revision FROM workbench_queries WHERE id=? AND kind=?`, id, kind).Scan(&data, &status, &rev) != nil {
		return nil, fmt.Errorf("查询申请不存在")
	}
	v := &WorkbenchQueryTicket{}
	if json.Unmarshal([]byte(data), v) != nil {
		return nil, fmt.Errorf("查询申请损坏")
	}
	v.ID = id
	v.Revision = rev
	v.Status = status
	return v, nil
}
func (s *Store) updateQueryTicket(v *WorkbenchQueryTicket, previous string) error {
	data, _ := json.Marshal(v)
	result, err := s.db.Exec(`UPDATE workbench_queries SET document=?,status=?,revision=revision+1 WHERE id=? AND revision=? AND status=?`, string(data), v.Status, v.ID, v.Revision, previous)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("查询申请状态已变化，请刷新")
	}
	v.Revision++
	return nil
}
func queryActor(r *http.Request) string {
	if name := r.Header.Get("X-User"); name != "" {
		return name
	}
	return "local"
}

// Called only through the capability facade. None of these routes can run writes.
func (s *Store) handleWorkbenchQueries(w http.ResponseWriter, r *http.Request, kind, rest string, raw []byte) bool {
	parts := strings.Split(rest, "/")
	actor := queryActor(r)
	respond := func(v any, e error) { writeIntegrationJSON(w, v, e) }
	if rest == "query-target" && r.Method == http.MethodGet {
		service, env, alias := r.URL.Query().Get("service"), r.URL.Query().Get("environment"), r.URL.Query().Get("resource_alias")
		if s.GetEnvironment(env) == nil {
			respond(nil, fmt.Errorf("请选择有效环境"))
			return true
		}
		var c *ResourceConnection
		var err error
		if kind == "dbms" {
			c, err = s.sqlBinding(service, env, alias)
		} else {
			c, err = s.redisBinding(service, env, alias)
		}
		if err != nil {
			respond(nil, err)
			return true
		}
		respond(map[string]any{"connection": c.Name, "database": c.Config["database"], "db": c.Config["db"], "key_prefix": c.Config["key_prefix"], "environment": env, "service": service, "resource_alias": alias}, nil)
		return true
	}
	if rest == "policy" {
		env := r.URL.Query().Get("environment")
		if s.GetEnvironment(env) == nil {
			respond(nil, fmt.Errorf("请选择有效环境"))
			return true
		}
		if r.Method == "GET" {
			p, e := s.workbenchPolicy(kind, env)
			respond(p, e)
			return true
		}
		if r.Method != "PUT" {
			http.Error(w, "method not allowed", 405)
			return true
		}
		if r.Header.Get("X-Role") != "admin" {
			http.Error(w, "administrator required", 403)
			return true
		}
		var input struct {
			QueryApproval  *bool `json:"query_approval"`
			ChangeApproval *bool `json:"change_approval"`
		}
		if json.Unmarshal(raw, &input) != nil || input.QueryApproval == nil || input.ChangeApproval == nil {
			respond(nil, fmt.Errorf("审批设置无效"))
			return true
		}
		p := WorkbenchPolicy{QueryApproval: *input.QueryApproval, ChangeApproval: *input.ChangeApproval}
		data, _ := json.Marshal(p)
		_, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "workbench_policy:"+kind+":"+env, string(data))
		if err == nil {
			s.Audit(actor, "configure", "workbench-policy", kind+":"+env, p)
		}
		respond(p, err)
		return true
	}
	if parts[0] != "query" && parts[0] != "query-requests" {
		return false
	}
	if rest == "query" && r.Method == "POST" {
		var q WorkbenchQuery
		err := json.Unmarshal(raw, &q)
		if err == nil {
			_, _, err = s.queryTargets(kind, q)
		}
		if err == nil {
			var required bool
			required, err = s.queryNeedsReview(kind, q)
			if required {
				err = fmt.Errorf("该环境查询需要审批，请先提交查询申请")
			}
		}
		if err != nil {
			respond(nil, err)
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		v, e := s.executeWorkbenchQuery(ctx, kind, q, actor)
		respond(v, e)
		return true
	}
	if rest == "query-requests" && r.Method == "GET" {
		rows, err := s.db.Query(`SELECT id FROM workbench_queries WHERE kind=? ORDER BY id DESC LIMIT 200`, kind)
		if err != nil {
			respond(nil, err)
			return true
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		out := []*WorkbenchQueryTicket{}
		for _, id := range ids {
			v, e := s.queryTicket(id, kind)
			if e == nil {
				out = append(out, v)
			}
		}
		respond(out, nil)
		return true
	}
	if rest == "query-requests" && r.Method == "POST" {
		var q WorkbenchQuery
		if json.Unmarshal(raw, &q) != nil {
			respond(nil, fmt.Errorf("查询参数无效"))
			return true
		}
		approvers, digest, err := s.queryTargets(kind, q)
		if err != nil {
			respond(nil, err)
			return true
		}
		for _, a := range approvers {
			if strings.TrimSpace(a) == "" {
				respond(nil, fmt.Errorf("请先配置相关环境的查询审核人"))
				return true
			}
		}
		v := &WorkbenchQueryTicket{Kind: kind, Query: q, Status: "pending_approval", Owner: actor, Approvers: approvers, TargetDigest: digest, CreatedAt: time.Now().Unix(), Revision: 1}
		data, _ := json.Marshal(v)
		res, e := s.db.Exec(`INSERT INTO workbench_queries(kind,document,status,owner,revision) VALUES(?,?,?,?,1)`, kind, string(data), v.Status, actor)
		if e == nil {
			v.ID, _ = res.LastInsertId()
			s.Audit(actor, "submit", "workbench-query", fmt.Sprint(v.ID), nil)
		}
		respond(v, e)
		return true
	}
	if len(parts) != 3 || r.Method != "POST" {
		http.NotFound(w, r)
		return true
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	v, err := s.queryTicket(id, kind)
	if err != nil {
		respond(nil, err)
		return true
	}
	if parts[2] == "approve" || parts[2] == "reject" {
		if v.Status != "pending_approval" || actor == v.Owner {
			respond(nil, fmt.Errorf("查询须由另一名审核人审核"))
			return true
		}
		for env, a := range v.Approvers {
			current := s.GetEnvironment(env)
			if current == nil || !allowedApprover(a, actor) || !allowedApprover(current.Approvers, actor) {
				respond(nil, fmt.Errorf("无相关环境审核权限"))
				return true
			}
		}
		v.Status = "rejected"
		if parts[2] == "approve" {
			v.Status = "approved"
			v.Approver = actor
		}
		err = s.updateQueryTicket(v, "pending_approval")
		if err == nil {
			s.Audit(actor, v.Status, "workbench-query", fmt.Sprint(id), nil)
		}
		respond(v, err)
		return true
	}
	if parts[2] != "execute" {
		http.NotFound(w, r)
		return true
	}
	if v.Status != "approved" || v.Owner != actor {
		respond(nil, fmt.Errorf("仅申请人可执行已批准查询，且只能执行一次"))
		return true
	}
	if time.Now().Unix()-v.CreatedAt > 1800 {
		respond(nil, fmt.Errorf("查询申请已超过 30 分钟，请重新申请"))
		return true
	}
	current, digest, err := s.queryTargets(kind, v.Query)
	if err == nil && digest != v.TargetDigest {
		err = fmt.Errorf("目标连接或任务已变化，请重新提交查询")
	}
	if err == nil {
		for env, a := range current {
			if v.Approver == v.Owner || !allowedApprover(a, v.Approver) || !allowedApprover(v.Approvers[env], v.Approver) {
				err = fmt.Errorf("查询审核权限已变化，请重新申请")
				break
			}
		}
	}
	if err != nil {
		respond(nil, err)
		return true
	}
	v.Status = "executing"
	if err = s.updateQueryTicket(v, "approved"); err != nil {
		respond(nil, err)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, execErr := s.executeWorkbenchQuery(ctx, kind, v.Query, actor)
	v.Status = "completed"
	if execErr != nil {
		v.Status = "failed"
	} else if cmp, ok := result.(*DataSyncComparison); ok {
		v.ResultDigest = cmp.Digest
	}
	if err = s.updateQueryTicket(v, "executing"); err != nil {
		respond(nil, fmt.Errorf("结果状态保存失败，请人工核对，不要重复执行"))
		return true
	}
	s.Audit(actor, v.Status, "workbench-query", fmt.Sprint(id), nil)
	if execErr != nil {
		respond(nil, execErr)
	} else {
		respond(map[string]any{"ticket": v, "result": result}, nil)
	}
	return true
}

// Comparison and freeze re-read both databases. An approved comparison permits
// only freezing that exact result by its owner; it never authorizes SQL writes.
func (s *Store) authorizeWorkbenchComparison(r *http.Request, raw []byte, id int64) error {
	var scope DataSyncScope
	if json.Unmarshal(raw, &scope) != nil {
		return fmt.Errorf("对比参数无效")
	}
	required, err := s.queryNeedsReview("dbms", WorkbenchQuery{TaskID: id, Scope: scope})
	if err != nil || !required {
		return err
	}
	var req struct {
		QueryApprovalID int64  `json:"query_approval_id"`
		Digest          string `json:"digest"`
	}
	json.Unmarshal(raw, &req)
	if !strings.HasSuffix(r.URL.Path, "/freeze") {
		return fmt.Errorf("数据对比涉及的查询需要审批，请到查询申请提交数据对比申请")
	}
	v, err := s.queryTicket(req.QueryApprovalID, "dbms")
	if err != nil || v.Status != "completed" || v.Owner != queryActor(r) || v.Query.TaskID != id || v.Query.Scope != scope || v.ResultDigest != req.Digest || time.Now().Unix()-v.CreatedAt > 1800 {
		return fmt.Errorf("需要本次数据对比的有效查询批准记录")
	}
	approvers, digest, err := s.queryTargets("dbms", v.Query)
	if err != nil {
		return err
	}
	if digest != v.TargetDigest {
		return fmt.Errorf("数据对比目标已变化，请重新申请")
	}
	for env, a := range approvers {
		if !allowedApprover(a, v.Approver) || !allowedApprover(v.Approvers[env], v.Approver) {
			return fmt.Errorf("数据对比审核权限已变化")
		}
	}
	return nil
}
