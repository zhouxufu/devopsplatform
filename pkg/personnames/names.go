// Package personnames resolves presentation labels without changing user identity.
package personnames

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Header is replaced by the trusted platform gateway, never used for permissions.
const Header = "X-User-Display-Names"

type Directory map[string]string

func FromRequest(r *http.Request) Directory {
	raw := r.Header.Get(Header)
	var names Directory
	if len(raw) > 60000 || json.Unmarshal([]byte(raw), &names) != nil {
		return nil
	}
	return names
}

func (d Directory) Name(account string) string {
	if name := strings.TrimSpace(d[account]); name != "" {
		return name
	}
	return account
}
