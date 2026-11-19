package deploy

import (
	"net/http"
)

// Registration to an environment is authoritative; connection names and client
// supplied profiles are not environment grants. This only enables bounded,
// source-verified discovery, not raw reads, writes, or connection maintenance.
func (s *Store) resourceConnectionApprovedForEnvironment(connection, environment string) bool {
	if s.GetEnvironment(environment) == nil {
		return false
	}
	if connection != "" && s.environmentNacos(environment) == connection {
		return true
	}
	rows, err := s.db.Query(`SELECT name FROM resource_applications ORDER BY name`)
	if err != nil {
		return false
	}
	names := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names = append(names, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false
	}
	for _, name := range names {
		resources := s.ApplicationResources(name)
		if resources.Error != "" {
			continue
		}
		for _, binding := range resources.Bindings {
			if binding.Environment == environment && binding.Connection == connection {
				return true
			}
		}
	}
	return false
}

func registerResourceDiscoveryAccessRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/services/{name}/resources/discovery-connections", func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "首次接入候选需要管理员权限")
			return
		}
		env := r.URL.Query().Get("environment")
		if !s.hasResourceApplication(r.PathValue("name")) || s.GetEnvironment(env) == nil {
			deliveryWriteError(w, 400, "必须明确存在的应用和环境")
			return
		}
		out := []map[string]any{}
		for _, c := range s.Connections() {
			if c.Kind == "nacos" && s.resourceConnectionApprovedForEnvironment(c.Name, env) {
				out = append(out, s.connectionMetadata(c.Name))
			}
		}
		deliveryJSON(w, out)
	})
}
