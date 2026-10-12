package deploy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type BuildListFilter struct {
	Service, Query, Status, Branch, Actor, Environment string
	Start, End                                         int64
	Page, PageSize                                     int
}
type BuildListPage struct {
	Items    []any `json:"items"`
	Total    int   `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

func (s *Store) ensureBuildWorkbenchIndexes() error {
	for _, v := range []struct{ table, name, columns string }{
		{"pipeline_runs", "idx_build_runs_service", "service,id"},
		{"artifacts", "idx_build_artifacts_service", "service,id"},
		{"release_orders", "idx_build_releases_service", "service,id"},
		{"release_orders", "idx_build_artifact_releases", "artifact_id,status"},
	} {
		statement := "CREATE INDEX IF NOT EXISTS " + v.name + " ON " + v.table + "(" + v.columns + ")"
		if s.db.dialect == "mysql" {
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?`, v.table, v.name).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			statement = "CREATE INDEX " + v.name + " ON " + v.table + "(" + v.columns + ")"
		}
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func buildLike(value string) string {
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value) + "%"
}
func (s *Store) BuildList(kind string, f BuildListFilter) (BuildListPage, error) {
	out := BuildListPage{Items: []any{}, Page: f.Page, PageSize: f.PageSize}
	if out.Page == 0 {
		out.Page = 1
	}
	if out.PageSize == 0 {
		out.PageSize = 20
	}
	if out.Page < 1 || out.Page > 100000 || out.PageSize < 1 || out.PageSize > 100 || f.Start < 0 || f.End < 0 || (f.End > 0 && f.Start > f.End) || len(f.Query) > 200 || len(f.Service) > 191 || len(f.Branch) > 300 || len(f.Actor) > 191 {
		return out, fmt.Errorf("记录筛选参数无效")
	}
	table, date, columns := "pipeline_runs", "started_at", []string{"service", "version", "branch", "actor"}
	switch kind {
	case "runs":
	case "artifacts":
		table, date, columns = "artifacts", "created_at", []string{"service", "version", "digest", "metadata"}
	case "releases":
		table, date, columns = "release_orders", "started_at", []string{"service", "version", "environment", "actor", "reason"}
	default:
		return out, fmt.Errorf("记录类型无效")
	}
	where, args := []string{"1=1"}, []any{}
	add := func(expr string, value any) { where = append(where, expr); args = append(args, value) }
	if f.Service != "" {
		add("service=?", f.Service)
	}
	if f.Query != "" {
		parts := []string{}
		for _, c := range columns {
			parts = append(parts, "COALESCE("+c+",'') LIKE ? ESCAPE '!'")
			args = append(args, buildLike(f.Query))
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if f.Status != "" && kind != "artifacts" {
		add("status=?", f.Status)
	}
	if f.Branch != "" {
		if kind == "runs" {
			add("branch=?", f.Branch)
		} else if kind == "artifacts" {
			add("COALESCE(metadata,'') LIKE ? ESCAPE '!'", buildLike(f.Branch))
		}
	}
	if f.Actor != "" && kind != "artifacts" {
		add("actor=?", f.Actor)
	}
	if f.Environment != "" && kind == "releases" {
		add("environment=?", f.Environment)
	}
	if f.Start > 0 {
		add(date+">=?", f.Start)
	}
	if f.End > 0 {
		add(date+"<=?", f.End)
	}
	clause := " FROM " + table + " WHERE " + strings.Join(where, " AND ")
	if err := s.db.QueryRow("SELECT COUNT(*)"+clause, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.db.Query("SELECT id"+clause+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, out.PageSize, (out.Page-1)*out.PageSize)...)
	if err != nil {
		return out, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Close the cursor before detail lookups (SQLite can use a single connection).
	for _, id := range ids {
		switch kind {
		case "runs":
			if v := s.GetRun(id); v != nil {
				v.BuildCommand = ""
				out.Items = append(out.Items, v)
			}
		case "artifacts":
			if v := s.GetArtifact(id); v != nil {
				if v.Metadata != nil {
					delete(v.Metadata, "build_command")
				}
				environments := []string{}
				rs, err := s.db.Query(`SELECT DISTINCT environment FROM release_orders WHERE artifact_id=? AND service=? AND status='success' ORDER BY environment`, id, v.Service)
				if err != nil {
					return out, err
				}
				for rs.Next() {
					var env string
					if err = rs.Scan(&env); err != nil {
						rs.Close()
						return out, err
					}
					environments = append(environments, env)
				}
				err = rs.Err()
				rs.Close()
				if err != nil {
					return out, err
				}
				out.Items = append(out.Items, struct {
					*Artifact
					PublishedEnvironments []string `json:"published_environments"`
				}{v, environments})
			}
		case "releases":
			if v := s.GetReleaseOrder(id); v != nil {
				out.Items = append(out.Items, v)
			}
		}
	}
	return out, nil
}

type BuildApplicationSummary struct {
	Delivery          *ServiceDelivery     `json:"delivery"`
	Name              string               `json:"name"`
	LatestRun         *PipelineRun         `json:"latest_run"`
	LatestArtifact    *Artifact            `json:"latest_artifact"`
	Environments      []ServiceEnvironment `json:"environments"`
	ReadyEnvironments int                  `json:"ready_environments"`
}

func (s *Store) BuildApplicationSummaries() ([]BuildApplicationSummary, error) {
	rows, err := s.db.Query(`SELECT name,COALESCE((SELECT MAX(id) FROM pipeline_runs r WHERE r.service=services.name),0),COALESCE((SELECT MAX(id) FROM artifacts a WHERE a.service=services.name),0) FROM services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	type source struct {
		name          string
		run, artifact int64
	}
	sources := []source{}
	for rows.Next() {
		var v source
		if err = rows.Scan(&v.name, &v.run, &v.artifact); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []BuildApplicationSummary{}
	for _, v := range sources {
		item := BuildApplicationSummary{Name: v.name, LatestRun: s.GetRun(v.run), LatestArtifact: s.GetArtifact(v.artifact), Environments: s.ServiceEnvironments(v.name), Delivery: s.GetServiceDelivery(v.name)}
		if item.Delivery != nil {
			item.Delivery.WebhookToken = ""
		}
		if item.LatestRun != nil {
			item.LatestRun.BuildCommand = ""
		}
		if item.LatestArtifact != nil && item.LatestArtifact.Metadata != nil {
			delete(item.LatestArtifact.Metadata, "build_command")
		}
		for _, t := range item.Environments {
			ready := t.HostGroup != "" && len(t.Hosts) > 0
			if t.ManagedTarget() {
				ready = t.DeployConfig["connection"] != "" && t.DeployConfig["app_id"] != "" && t.DeployConfig["namespace_id"] != "" && t.DeployConfig["region"] != ""
			}
			if ready {
				item.ReadyEnvironments++
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func registerBuildWorkbenchRoutes(mux *http.ServeMux, s *Store, wj func(http.ResponseWriter, any), failure func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("GET /api/services/summary", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.BuildApplicationSummaries()
		if e != nil {
			failure(w, 500, "应用状态读取失败")
			return
		}
		wj(w, v)
	})
	for kind, path := range map[string]string{"runs": "/api/pipeline/runs/page", "artifacts": "/api/artifacts/page", "releases": "/api/release-orders/page"} {
		kind := kind
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			f := BuildListFilter{Service: q.Get("service"), Query: q.Get("q"), Status: q.Get("status"), Branch: q.Get("branch"), Actor: q.Get("actor"), Environment: q.Get("environment")}
			for key, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
				if q.Get(key) != "" {
					v, e := strconv.Atoi(q.Get(key))
					if e != nil {
						failure(w, 400, "分页参数无效")
						return
					}
					*target = v
				}
			}
			for key, target := range map[string]*int64{"start": &f.Start, "end": &f.End} {
				if q.Get(key) != "" {
					v, e := strconv.ParseInt(q.Get(key), 10, 64)
					if e != nil {
						failure(w, 400, "日期参数无效")
						return
					}
					*target = v
				}
			}
			v, e := s.BuildList(kind, f)
			if e != nil {
				failure(w, 400, e)
				return
			}
			wj(w, v)
		})
	}
}
