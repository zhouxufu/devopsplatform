package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func deliveryJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func deliveryWriteError(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprint(v)})
}
func deliveryActor(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-User")); v != "" {
		return v
	}
	return "local"
}
func deliveryManage(r *http.Request, owner string) bool {
	return deliveryActor(r) == "local" || deliveryActor(r) == owner || r.Header.Get("X-Role") == "admin"
}

type VersionArtifactBinding struct {
	ArtifactKind         string `json:"artifact_kind,omitempty"`
	BuildVariant         string `json:"build_variant,omitempty"`
	BuildIncompatibility string `json:"build_incompatibility,omitempty"`
	Service              string `json:"service"`
	ArtifactID           int64  `json:"artifact_id"`
	Digest               string `json:"digest"`
	Version              string `json:"version"`
	Branch               string `json:"branch"`
	BuildID              int64  `json:"build_id"`
	CodeConfirmed        bool   `json:"code_confirmed"`
}
type VersionArtifacts struct {
	Environment string                   `json:"environment,omitempty"`
	VersionID   int64                    `json:"version_id"`
	Bindings    []VersionArtifactBinding `json:"bindings"`
	ScopeDigest string                   `json:"scope_digest"`
	Owner       string                   `json:"owner"`
	UpdatedAt   int64                    `json:"updated_at"`
	Locked      bool                     `json:"locked"`
}

func versionCodeScope(v *ReleaseVersion) string {
	code := []Change{}
	for _, c := range v.Changes {
		if c.Kind == "code" || c.Kind == "artifact" {
			code = append(code, c)
		}
	}
	return digestJSON(code)
}
func versionArtifactStorage(id int64, environment string) (string, string, []any) {
	if environment == "" {
		return "delivery_version_artifacts", "version_id=?", []any{id}
	}
	return "delivery_environment_artifacts", "version_id=? AND environment=?", []any{id, environment}
}
func (s *Store) VersionArtifacts(id int64, environments ...string) VersionArtifacts {
	environment := ""
	if len(environments) > 0 {
		environment = environments[0]
	}
	out := VersionArtifacts{VersionID: id, Bindings: []VersionArtifactBinding{}}
	out.Environment = environment
	var raw string
	var used int
	table, where, args := versionArtifactStorage(id, environment)
	if s.db.QueryRow(`SELECT document,used FROM `+table+` WHERE `+where, args...).Scan(&raw, &used) == nil {
		json.Unmarshal([]byte(raw), &out)
		out.Locked = used == 1
	} else if environment != "" {
		legacy := s.VersionArtifacts(id)
		if len(legacy.Bindings) > 0 {
			compatible := true
			for _, b := range legacy.Bindings {
				if s.validateArtifactBuildVariant(s.GetArtifact(b.ArtifactID), environment) != nil {
					compatible = false
					break
				}
			}
			if compatible {
				return legacy
			}
		}
	}
	return out
}
func (s *Store) SaveVersionArtifacts(id int64, bindings []VersionArtifactBinding, actor string, environments ...string) (VersionArtifacts, error) {
	environment := ""
	if len(environments) > 0 {
		environment = environments[0]
	}
	out := VersionArtifacts{}
	if environment != "" && s.GetEnvironment(environment) == nil {
		return out, fmt.Errorf("目标环境不存在")
	}
	if environment != "" {
		previous := s.VersionArtifacts(id, environment)
		if previous.Environment == "" && previous.Locked {
			return out, fmt.Errorf("此环境沿用的版本制品已锁定，请创建新版本")
		}
	}
	v := s.GetReleaseVersion(id)
	if v == nil || v.Status == ReleaseVersionCanceled {
		return out, fmt.Errorf("发布版本不可用")
	}
	expected := map[string]bool{}
	for _, c := range v.Changes {
		if c.Kind == "code" || c.Kind == "artifact" {
			expected[c.Service] = true
		}
	}
	if len(bindings) != len(expected) {
		return out, fmt.Errorf("每个代码应用必须且只能关联一个制品")
	}
	seen := map[string]bool{}
	for i, b := range bindings {
		a := s.GetArtifact(b.ArtifactID)
		sv := s.GetService(b.Service)
		if seen[b.Service] || !expected[b.Service] || a == nil || sv == nil || a.Service != b.Service || a.Digest == "" || !s.artifactMatchesEnvironment(a, sv, environment) || !b.CodeConfirmed {
			return out, fmt.Errorf("请核对应用制品并人工确认包含本版本代码变更")
		}
		seen[b.Service] = true
		if environment != "" {
			if err := s.validateArtifactBuildVariant(a, environment); err != nil {
				return out, err
			}
		}
		b.ArtifactKind = a.Kind
		b.BuildVariant, b.BuildIncompatibility = a.Metadata["build_variant"], ""
		b.Digest, b.Version = a.Digest, a.Version
		b.BuildID = 0
		b.Branch = ""
		if a.Source == "pipeline-run" {
			r := s.GetRun(a.SourceID)
			if r == nil || r.Service != a.Service || r.ArtifactID != a.ID || r.Status != "success" {
				return out, fmt.Errorf("制品来源构建未成功或关联已改变")
			}
			b.BuildID, b.Branch = r.ID, r.Branch
		}
		bindings[i] = b
	}
	out = VersionArtifacts{VersionID: id, Environment: environment, Bindings: bindings, ScopeDigest: versionCodeScope(v), Owner: actor, UpdatedAt: time.Now().Unix()}
	raw, _ := json.Marshal(out)
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	artifactIDs := []int64{}
	for _, binding := range bindings {
		artifactIDs = append(artifactIDs, binding.ArtifactID)
	}
	if err := lockArtifactReferences(tx, artifactIDs...); err != nil {
		return out, err
	}
	var existing string
	var used int
	table, where, args := versionArtifactStorage(id, environment)
	err = tx.QueryRow(`SELECT document,used FROM `+table+` WHERE `+where, args...).Scan(&existing, &used)
	if err == nil && used == 1 {
		return out, fmt.Errorf("此版本已生成交付单，制品映射已锁定；变更制品请创建新版本")
	}
	// Upsert does not overwrite a mapping concurrently locked by a submission.
	if err == sql.ErrNoRows {
		if environment == "" {
			_, err = tx.Exec(`INSERT INTO delivery_version_artifacts(version_id,document,used) VALUES(?,?,0)`, id, string(raw))
		} else {
			_, err = tx.Exec(`INSERT INTO delivery_environment_artifacts(version_id,environment,document,used) VALUES(?,?,?,0)`, id, environment, string(raw))
		}
	} else if err != nil {
		return out, err
	} else {
		var n int64
		r, e := tx.Exec(`UPDATE `+table+` SET document=? WHERE `+where+` AND used=0`, append([]any{string(raw)}, args...)...)
		err = e
		if err == nil {
			n, _ = r.RowsAffected()
			if n != 1 {
				err = fmt.Errorf("版本制品关联已变化，请重新读取")
			}
		}
	}
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	s.Audit(actor, "pin-artifacts", "release-version", fmt.Sprint(id), nil)
	return out, nil
}

type DeliveryRunSummary struct {
	Preparation   bool   `json:"preparation"`
	CurrentKind   string `json:"current_kind,omitempty"`
	NextAction    string `json:"next_action,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	UpdatedAt     int64  `json:"updated_at"`

	ScopeSummary    string `json:"scope_summary,omitempty"`
	SourceType      string `json:"source_type,omitempty"`
	ID              int64  `json:"id"`
	TemplateID      int64  `json:"template_id"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	EnvironmentName string `json:"environment_name"`
	VersionID       int64  `json:"version_id"`
	Version         string `json:"version"`
	Owner           string `json:"owner"`
	Approver        string `json:"approver"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"created_at"`
	Applications    int    `json:"applications"`
	Completed       int    `json:"completed"`
	TotalSteps      int    `json:"total_steps"`
	CurrentNode     string `json:"current_node"`
}
type DeliveryRunPage struct {
	Items    []DeliveryRunSummary `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

func (s *Store) DeliveryRunPage(q, status, environment, version, owner string, start, end int64, page, size int) (DeliveryRunPage, error) {
	return s.deliveryRunPage(q, status, environment, version, owner, start, end, page, size, "")
}

func (s *Store) deliveryRunPage(q, status, environment, version, owner string, start, end int64, page, size int, actor string) (DeliveryRunPage, error) {
	out := DeliveryRunPage{Items: []DeliveryRunSummary{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || (size != 20 && size != 50 && size != 100) || len(q) > 256 || len(owner) > 191 || start < 0 || end < 0 || (end > 0 && start > end) {
		return out, fmt.Errorf("筛选或分页参数无效")
	}
	field := func(p string) string {
		if s.Driver() == "mysql" {
			return "JSON_UNQUOTE(JSON_EXTRACT(plan,'" + p + "'))"
		}
		return "json_extract(plan,'" + p + "')"
	}
	where, args := []string{"1=1"}, []any{}
	for _, f := range [][2]string{{"owner", owner}, {field("$.environment.name"), environment}} {
		if f[1] != "" {
			where = append(where, f[0]+"=?")
			args = append(args, f[1])
		}
	}
	if version != "" && version != "0" {
		id, err := strconv.ParseInt(version, 10, 64)
		if err != nil || id <= 0 {
			return out, fmt.Errorf("版本筛选参数无效")
		}
		where = append(where, field("$.version.id")+"=?")
		args = append(args, id)
	}
	if start > 0 {
		where = append(where, "created_at>=?")
		args = append(args, start)
	}
	if end > 0 {
		where = append(where, "created_at<=?")
		args = append(args, end)
	}
	if q != "" {
		where = append(where, "("+field("$.template.name")+" LIKE ? OR "+field("$.version.version")+" LIKE ? OR owner LIKE ?)")
		for i := 0; i < 3; i++ {
			args = append(args, "%"+q+"%")
		}
	}
	switch status {
	case "my_reviews":
		where = append(where, "status='pending_approval'")
	case "in_progress":
		where = append(where, "status IN ('pending_approval','waiting_confirmation','approved','running','blocked','paused','waiting_manual','waiting_next_batch')")
	case "":
	default:
		where = append(where, "(status=? OR status IN ('running','blocked'))")
		args = append(args, status)
	}
	clause := strings.Join(where, " AND ")
	project := func(r *DeliveryRun, t ResearchTask) DeliveryRunSummary {
		v := DeliveryRunSummary{ID: r.ID, TemplateID: r.Plan.Template.ID, Name: r.Plan.Template.Name, Environment: r.Plan.Environment.Name, EnvironmentName: r.Plan.Environment.DisplayName, Owner: r.Owner, Approver: r.Approver, Status: r.Status, CreatedAt: r.CreatedAt, Applications: len(r.Plan.Services), TotalSteps: len(r.Steps), Preparation: r.Plan.PreparationRequest != nil, NextAction: t.NextAction, BlockedReason: t.BlockedReason, UpdatedAt: t.UpdatedAt}
		if r.Plan.Scope != nil {
			v.SourceType = r.Plan.Scope.Type
			if v.SourceType == "work_items" {
				v.ScopeSummary = fmt.Sprintf("需求 / Bug · %d 项", len(r.Plan.Scope.WorkItems))
			}
			if v.SourceType == "changes" {
				v.ScopeSummary = fmt.Sprintf("所选变更 · %d 项", len(r.Plan.Scope.Changes))
			}
		}
		if v.Applications == 0 && r.Plan.Scope != nil {
			apps := map[string]bool{}
			for _, c := range r.Plan.Scope.Changes {
				if (c.Kind == "code" || c.Kind == "artifact") && c.Service != "" {
					apps[c.Service] = true
				}
			}
			v.Applications = len(apps)
		}
		if r.Plan.Version != nil {
			v.VersionID = r.Plan.Version.ID
			v.Version = r.Plan.Version.Version
		}
		for _, step := range r.Steps {
			if scheduleStepDone(step) {
				v.Completed++
			} else if v.CurrentNode == "" {
				v.CurrentNode = step.Name
				v.CurrentKind = step.Kind
			}
		}
		return v
	}
	readBatch := func(query string, params []any) ([]*DeliveryRun, error) {
		rows, err := s.db.Query(query, params...)
		if err != nil {
			return nil, err
		}
		items := []*DeliveryRun{}
		for rows.Next() {
			r, e := scanDeliveryRun(rows)
			if e != nil {
				rows.Close()
				return nil, e
			}
			items = append(items, r)
		}
		err = rows.Err()
		rows.Close()
		return items, err
	}
	if status == "" {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_runs WHERE `+clause, args...).Scan(&out.Total); err != nil {
			return out, err
		}
		rows, err := readBatch(deliveryRunSelect+` WHERE `+clause+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), size, (page-1)*size))
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			out.Items = append(out.Items, project(r, s.researchSummary(r)))
		}
		return out, nil
	}
	// Filter the same effective status that detail/tracking reads, before counting
	// and pagination. Keyset batches keep memory bounded and do not revisit new IDs.
	var before int64
	for {
		query := deliveryRunSelect + ` WHERE ` + clause
		params := append([]any{}, args...)
		if before > 0 {
			query += ` AND id<?`
			params = append(params, before)
		}
		rows, err := readBatch(query+` ORDER BY id DESC LIMIT 100`, params)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			task := s.researchSummary(r)
			v := project(r, task)
			switch status {
			case "my_reviews":
				if actor == "" || actor == v.Owner || v.Status != "pending_approval" || task.Kind != "approval" || !slices.Contains(task.Assignees, actor) {
					continue
				}
			case "in_progress":
				switch v.Status {
				case "pending_approval", "waiting_confirmation", "approved", "running", "blocked", "paused", "waiting_manual", "waiting_next_batch":
				default:
					continue
				}
			default:
				if v.Status != status {
					continue
				}
			}
			out.Total++
			if out.Total > (page-1)*size && len(out.Items) < size {
				out.Items = append(out.Items, v)
			}
		}
		if len(rows) < 100 {
			break
		}
		before = rows[len(rows)-1].ID
	}
	return out, nil
}

func registerDeliveryExperienceRoutes(mux *http.ServeMux, s *Store, m *ReleaseManager) {
	registerReleaseReadinessRoutes(mux, s)
	mux.HandleFunc("GET /api/delivery/runs/{id}/rollback-candidate", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		releaseID, _ := strconv.ParseInt(r.URL.Query().Get("release_id"), 10, 64)
		run := s.DeliveryRun(id)
		if run == nil {
			deliveryWriteError(w, 404, "执行单不存在")
			return
		}
		allowed := false
		for _, step := range run.Steps {
			allowed = allowed || (releaseID > 0 && step.ReleaseID == releaseID)
		}
		order := s.GetReleaseOrder(releaseID)
		if !allowed || order == nil || order.Status != "success" {
			deliveryWriteError(w, 409, "仅本次交付内成功的子发布单可生成回退候选")
			return
		}
		var artifactID int64
		ids := s.ReleaseDeployIDs(releaseID)
		for _, deployID := range ids {
			d := s.GetDeploy(deployID)
			if d == nil || d.Status != "success" {
				deliveryWriteError(w, 409, "目标结果不完整，请逐实例核对")
				return
			}
			previous := s.PrevSuccess(d.Service, d.Host, d.ID)
			if previous == nil || previous.Environment != order.Environment || previous.ArtifactID <= 0 || (artifactID > 0 && artifactID != previous.ArtifactID) {
				deliveryWriteError(w, 409, "各实例旧制品缺失或不一致，请逐实例制定恢复方案")
				return
			}
			artifactID = previous.ArtifactID
		}
		a := s.GetArtifact(artifactID)
		if len(ids) == 0 || a == nil || a.Digest == "" {
			deliveryWriteError(w, 409, "旧制品不可用，请人工核对")
			return
		}
		deliveryJSON(w, VersionArtifactBinding{Service: a.Service, ArtifactID: a.ID, Digest: a.Digest, Version: a.Version})
	})
	mux.HandleFunc("GET /api/delivery/runs/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, size := 1, 20
		var err error
		if q.Get("page") != "" {
			page, err = strconv.Atoi(q.Get("page"))
			if err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
		}
		if q.Get("page_size") != "" {
			size, err = strconv.Atoi(q.Get("page_size"))
			if err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
		}
		start, end := int64(0), int64(0)
		for key, dst := range map[string]*int64{"start": &start, "end": &end} {
			if q.Get(key) != "" {
				*dst, err = strconv.ParseInt(q.Get(key), 10, 64)
				if err != nil {
					deliveryWriteError(w, 400, err)
					return
				}
			}
		}
		v, e := s.deliveryRunPage(q.Get("q"), q.Get("status"), q.Get("environment"), q.Get("version_id"), q.Get("owner"), start, end, page, size, deliveryActor(r))
		if e != nil {
			deliveryWriteError(w, 400, e)
			return
		}
		deliveryJSON(w, v)
	})
	mux.HandleFunc("GET /api/delivery/runs/owners", func(w http.ResponseWriter, r *http.Request) {
		out := []string{}
		rows, e := s.db.Query(`SELECT DISTINCT owner FROM delivery_runs WHERE owner<>'' ORDER BY owner`)
		if e != nil {
			deliveryWriteError(w, 500, e)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			rows.Scan(&v)
			out = append(out, v)
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/release-versions/{id}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if s.GetReleaseVersion(id) == nil {
			deliveryWriteError(w, 404, "版本不存在")
			return
		}
		deliveryJSON(w, s.VersionArtifacts(id, r.URL.Query().Get("environment")))
	})
	mux.HandleFunc("PUT /api/release-versions/{id}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v := s.GetReleaseVersion(id)
		if v == nil || !deliveryManage(r, v.Owner) {
			deliveryWriteError(w, 403, "仅版本创建人或管理员可关联制品")
			return
		}
		var body struct {
			Environment string                   `json:"environment"`
			Bindings    []VersionArtifactBinding `json:"bindings"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
			deliveryWriteError(w, 400, "制品关联格式错误")
			return
		}
		out, err := s.SaveVersionArtifacts(id, body.Bindings, deliveryActor(r), body.Environment)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/delivery/artifacts", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		service := q.Get("service")
		if s.GetService(service) == nil {
			deliveryWriteError(w, 400, "请选择有效应用")
			return
		}
		out := []VersionArtifactBinding{}
		page := 1
		if q.Get("page") != "" {
			page, _ = strconv.Atoi(q.Get("page"))
		}
		if page < 1 || page > 1000000 || len(q.Get("q")) > 256 {
			deliveryWriteError(w, 400, "制品筛选参数无效")
			return
		}
		where := "a.service=? AND NOT EXISTS (SELECT 1 FROM artifact_cleanup c WHERE c.artifact_id=a.id) AND (a.source<>'pipeline-run' OR (r.status='success' AND (r.artifact_id=a.id OR EXISTS (SELECT 1 FROM build_artifact_outputs o WHERE o.run_id=r.id AND o.artifact_id=a.id AND o.digest=a.digest AND o.parent_artifact_id=r.artifact_id AND o.role='compilation'))))"
		args := []any{service}
		if term := q.Get("q"); term != "" {
			where += " AND (a.version LIKE ? OR r.branch LIKE ?)"
			args = append(args, "%"+term+"%", "%"+term+"%")
		}
		from := " FROM artifacts a LEFT JOIN pipeline_runs r ON a.source='pipeline-run' AND r.id=a.source_id WHERE " + where
		var total int
		if err := s.db.QueryRow("SELECT COUNT(*)"+from, args...).Scan(&total); err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		rows, err := s.db.Query("SELECT a.id,a.version,a.digest,COALESCE(r.id,0),COALESCE(r.branch,'')"+from+" ORDER BY a.id DESC LIMIT 50 OFFSET ?", append(args, (page-1)*50)...)
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			b := VersionArtifactBinding{Service: service}
			if err := rows.Scan(&b.ArtifactID, &b.Version, &b.Digest, &b.BuildID, &b.Branch); err != nil {
				deliveryWriteError(w, 500, err)
				return
			}
			out = append(out, b)
		}
		rows.Close() // release SQLite's cursor before provenance lookups
		for i := range out {
			a := s.GetArtifact(out[i].ArtifactID)
			if a != nil {
				out[i].BuildVariant = a.Metadata["build_variant"]
				out[i].ArtifactKind = a.Kind
			}
			if environment := q.Get("environment"); environment != "" {
				if !s.artifactMatchesEnvironment(a, s.GetService(service), environment) {
					out[i].BuildIncompatibility = "制品类型与目标环境发布方式不匹配"
				}
				if err := s.validateArtifactBuildVariant(a, environment); err != nil {
					out[i].BuildIncompatibility = err.Error()
				}
			}
		}
		if err := rows.Err(); err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		deliveryJSON(w, map[string]any{"items": out, "total": total, "page": page, "page_size": 50})
	})
	mux.HandleFunc("GET /api/delivery/runs/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		run := s.DeliveryRun(id)
		if run == nil {
			deliveryWriteError(w, 404, "执行单不存在")
			return
		}
		releaseID, _ := strconv.ParseInt(r.URL.Query().Get("release_id"), 10, 64)
		allowed := false
		for _, step := range run.Steps {
			if step.ReleaseID == releaseID && releaseID > 0 {
				allowed = true
			}
			for _, previous := range step.PreviousReleaseIDs {
				if previous == releaseID && releaseID > 0 {
					allowed = true
				}
			}
		}
		if !allowed {
			deliveryWriteError(w, 403, "子发布单不属于本次交付")
			return
		}
		out := []map[string]any{}
		values := []string{}
		for _, encrypted := range run.envSecrets {
			if env, err := s.decryptRuntimeEnvironment(encrypted); err == nil {
				for _, value := range env {
					if value != "" {
						values = append(values, value)
					}
				}
			}
		}
		sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
		ids := s.ReleaseDeployIDs(releaseID)
		page := 1
		if r.URL.Query().Get("page") != "" {
			page, _ = strconv.Atoi(r.URL.Query().Get("page"))
		}
		if page < 1 || page > 1000000 {
			deliveryWriteError(w, 400, "日志分页无效")
			return
		}
		start := (page - 1) * 20
		if start > len(ids) {
			start = len(ids)
		}
		end := start + 20
		if end > len(ids) {
			end = len(ids)
		}
		for _, deployID := range ids[start:end] {
			d := s.GetDeploy(deployID)
			if d == nil {
				continue
			}
			var log string
			s.db.QueryRow(`SELECT log FROM deploys WHERE id=?`, deployID).Scan(&log)
			if d.Status == "running" && m != nil && m.dp != nil {
				m.dp.mu.Lock()
				tail := m.dp.logs[deployID]
				m.dp.mu.Unlock()
				if tail != nil {
					log = tail.String()
				}
			}
			for _, value := range values {
				log = strings.ReplaceAll(log, value, "[REDACTED]")
			}
			if len(log) > 65536 {
				log = log[len(log)-65536:]
			}
			out = append(out, map[string]any{"id": d.ID, "host": d.Host, "status": d.Status, "log": log})
		}
		deliveryJSON(w, map[string]any{"items": out, "total": len(ids), "page": page})
	})
	registerDeliveryControlRoutes(mux, s, m)
}
