package platform

import (
	"net/http"
	"strings"
)

func researchPluginPathAllowed(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/git/branches" || p == "/api/work-item-attachments" || strings.HasPrefix(p, "/api/work-item-attachments/") {
		return true
	}
	if strings.HasPrefix(p, "/api/release-notices/version/") {
		return true
	}
	if p == "/ui" || strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/api/research/") {
		return true
	}
	for _, prefix := range []string{"/api/work-loops", "/api/work-items", "/api/changes", "/api/release-versions", "/api/delivery/code-preparations"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return r.Method == http.MethodGet && (p == "/api/delivery/runs" || p == "/api/services" || p == "/api/environments" || strings.HasPrefix(p, "/api/services/"))
}
