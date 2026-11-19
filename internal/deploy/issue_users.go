package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Directory is provider-neutral. Never forward raw upstream user records,
// permissions, password hashes, sessions or credentials to the platform.
type issueUser struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Name    string `json:"name"`
}
type issueDirectory struct {
	Source string      `json:"source"`
	Label  string      `json:"label"`
	Users  []issueUser `json:"users"`
}

func (s *Store) zentaoUsers(ctx context.Context, name string) (*issueDirectory, error) {
	c := s.GetConnection(name)
	if c == nil || c.Kind != "zentao" {
		return nil, fmt.Errorf("请选择禅道连接")
	}
	z, err := s.connectionZentao(ctx, c)
	if err != nil {
		return nil, err
	}
	result := &issueDirectory{Source: fmt.Sprintf("zentao:%x", sha256.Sum256([]byte(z.root))), Label: "禅道 · " + name, Users: []issueUser{}}
	seenIDs, seenAccounts := map[string]bool{}, map[string]bool{}
	for page := 1; page <= 100; page++ {
		var data struct {
			Users json.RawMessage `json:"users"`
		}
		// Verified against official zentaopms_16.0 company/control.php. Unlike
		// api/getModel, company/browse removes password fields on the server.
		err := z.call(ctx, "company", "browse", url.Values{"browseType": {"inside"}, "type": {"bydept"}, "param": {"0"}, "orderBy": {"id_asc"}, "recPerPage": {"100"}, "pageID": {strconv.Itoa(page)}}, nil, &data)
		if err != nil {
			return nil, fmt.Errorf("读取禅道用户失败（需要组织/用户列表查看权限）：%w", err)
		}
		var records []json.RawMessage
		if len(data.Users) == 0 || string(data.Users) == "null" {
			return nil, fmt.Errorf("禅道未返回 users 列表，请检查组织/用户列表权限")
		}
		if data.Users[0] == '[' {
			if json.Unmarshal(data.Users, &records) != nil {
				return nil, fmt.Errorf("禅道用户列表格式错误")
			}
		} else {
			var entries map[string]json.RawMessage
			if json.Unmarshal(data.Users, &entries) != nil {
				return nil, fmt.Errorf("禅道用户列表格式错误")
			}
			for _, record := range entries {
				records = append(records, record)
			}
		}
		for _, raw := range records {
			var u struct {
				ID      json.Number `json:"id"`
				Account string      `json:"account"`
				Name    string      `json:"realname"`
				Deleted json.Number `json:"deleted"`
			}
			if json.Unmarshal(raw, &u) != nil || u.ID.String() == "" || strings.TrimSpace(u.Account) == "" {
				return nil, fmt.Errorf("禅道用户记录缺少有效编号或账号")
			}
			id, err := strconv.ParseInt(u.ID.String(), 10, 64)
			if err != nil || id < 1 || seenIDs[u.ID.String()] || seenAccounts[u.Account] {
				return nil, fmt.Errorf("禅道用户分页返回重复或无效账号，未同步任何用户")
			}
			seenIDs[u.ID.String()], seenAccounts[u.Account] = true, true
			if u.Deleted.String() == "1" {
				continue
			}
			result.Users = append(result.Users, issueUser{ID: u.ID.String(), Account: u.Account, Name: u.Name})
		}
		if len(records) < 100 {
			sort.Slice(result.Users, func(i, j int) bool { return result.Users[i].Account < result.Users[j].Account })
			return result, nil
		}
	}
	return nil, fmt.Errorf("禅道用户超过分页上限，未同步任何用户")
}

func registerIssueUserRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /api/connections/{name}/user-directory", func(w http.ResponseWriter, r *http.Request) {
		// Standalone management listener uses local mode; the gateway replaces
		// these headers and requires admin for this connections mutation path.
		if role := r.Header.Get("X-Role"); role != "" && role != "admin" {
			http.Error(w, `{"error":"仅管理员可同步用户"}`, http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		directory, err := s.zentaoUsers(ctx, r.PathValue("name"))
		writeIntegrationJSON(w, directory, err)
	})
}
