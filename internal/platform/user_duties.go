package platform

import (
	"encoding/json"
	"fmt"
)

// Duties are optional personnel labels, never authorization roles.
func normalizeDuties(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	out, seen := []string{}, map[string]bool{}
	for _, v := range values {
		if v != "frontend" && v != "backend" && v != "test" && v != "product" {
			return nil, fmt.Errorf("职能仅支持前端、后端、测试、产品")
		}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}

func (r *Registry) userDuties(username string) []string {
	var raw string
	out := []string{}
	if r.db.QueryRow(`SELECT document FROM platform_user_duties WHERE username=?`, username).Scan(&raw) == nil {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// Used by the gateway to resolve external assignees. Clients cannot inject it.
func (r *Registry) UserIdentities() ([]UserIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userIdentitiesLocked()
}
