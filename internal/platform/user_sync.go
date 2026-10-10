package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type UserIdentity struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	Username   string `json:"username"`
}
type DirectoryUser struct {
	ID       string `json:"id"`
	Account  string `json:"account"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
	Username string `json:"username,omitempty"` // Current/resulting platform identity, not the remote account.
	Bound    bool   `json:"bound"`
}
type UserDirectory struct {
	Source string          `json:"source"`
	Label  string          `json:"label"`
	Users  []DirectoryUser `json:"users"`
}

func (r *Registry) userIdentitiesLocked() ([]UserIdentity, error) {
	rows, err := r.db.Query(`SELECT source,external_id,username FROM platform_user_identities ORDER BY source,external_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []UserIdentity{}
	for rows.Next() {
		var i UserIdentity
		if err := rows.Scan(&i.Source, &i.ExternalID, &i.Username); err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}

// Same-name local accounts are never implicitly adopted. External ids survive
// renames, while local usernames, roles, passwords and enable flags are stable.
func directoryAction(u DirectoryUser, source string, identities []UserIdentity, users []User) (string, string, string) {
	for _, i := range identities {
		if i.Source == source && i.ExternalID == u.ID {
			for _, existing := range users {
				if existing.Username == i.Username {
					return "update", "仅更新姓名；保留平台角色、密码和启用状态", i.Username
				}
			}
			return "skip", "平台账号已删除，不自动重新创建", i.Username
		}
	}
	if !usernameRE.MatchString(u.Account) || len([]rune(u.Name)) > 128 {
		return "skip", "用户名格式不兼容或姓名过长", ""
	}
	if strings.EqualFold(u.Account, "admin") || strings.EqualFold(u.Account, "local") {
		return "skip", "平台保留账号，不自动关联", ""
	}
	for _, existing := range users {
		if strings.EqualFold(existing.Username, u.Account) {
			return "skip", "已有同名平台账号，不自动覆盖或关联", ""
		}
	}
	for _, i := range identities {
		if strings.EqualFold(i.Username, u.Account) {
			return "skip", "用户名已由其他外部身份使用", ""
		}
	}
	return "create", "创建成员，待设置平台密码后启用", u.Account
}

func (r *Registry) SyncDirectory(d UserDirectory, selected []string, apply bool, requestedLinks ...map[string]string) (*UserDirectory, error) {
	if !strings.HasPrefix(d.Source, "zentao:") || len(d.Source) != 71 || len(d.Users) > 10000 {
		return nil, fmt.Errorf("用户来源无效")
	}
	if d.Users == nil {
		d.Users = []DirectoryUser{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	identities, err := r.userIdentitiesLocked()
	if err != nil {
		return nil, err
	}
	users, err := r.usersLocked()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, u := range d.Users {
		if u.ID == "" || len(u.ID) > 64 || known[u.ID] {
			return nil, fmt.Errorf("用户编号无效或重复")
		}
		known[u.ID] = true
	}
	selection := map[string]bool{}
	for _, id := range selected {
		if !known[id] {
			return nil, fmt.Errorf("选择的用户已不在禅道列表，请重新预览")
		}
		selection[id] = true
	}
	if apply && len(selection) == 0 {
		return nil, fmt.Errorf("请至少选择一个用户")
	}
	links := map[string]string{}
	if len(requestedLinks) > 0 && requestedLinks[0] != nil {
		links = requestedLinks[0]
	}
	targets := map[string]bool{}
	for id, username := range links {
		if !known[id] || (apply && !selection[id]) {
			return nil, fmt.Errorf("关联的来源用户必须在本次所选列表中，请重新读取用户")
		}
		found := false
		for _, u := range users {
			if u.Username == username {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("关联目标平台用户不存在，请重新读取用户")
		}
		if targets[username] {
			return nil, fmt.Errorf("同一平台用户不能关联多个来源用户")
		}
		targets[username] = true
		for _, identity := range identities {
			if identity.Source == d.Source && identity.ExternalID == id && identity.Username != username {
				return nil, fmt.Errorf("来源用户已关联其他平台账号，不能在同步中改绑")
			}
			if identity.Username == username && (identity.Source != d.Source || identity.ExternalID != id) {
				return nil, fmt.Errorf("平台用户已关联其他来源身份，请重新读取用户")
			}
		}
	}
	var tx *sql.Tx
	if apply {
		tx, err = r.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	for index, u := range d.Users {
		action, reason, username := directoryAction(u, d.Source, identities, users)
		d.Users[index].Bound = false
		for _, identity := range identities {
			if identity.Source == d.Source && identity.ExternalID == u.ID {
				d.Users[index].Bound = true
				break
			}
		}
		if target, ok := links[u.ID]; ok {
			action, reason, username = "link", "仅建立身份关联；本次保留平台姓名、角色、密码和启用状态", target
		}
		d.Users[index].Action, d.Users[index].Reason, d.Users[index].Username = action, reason, username
		if !apply || !selection[u.ID] || action == "skip" {
			continue
		}
		if action == "link" {
			exists := false
			for _, identity := range identities {
				if identity.Source == d.Source && identity.ExternalID == u.ID {
					exists = true
					break
				}
			}
			if !exists {
				if _, err = tx.Exec(`INSERT INTO platform_user_identities(source,external_id,username) VALUES(?,?,?)`, d.Source, u.ID, username); err != nil {
					return nil, fmt.Errorf("关联冲突，未保存此次同步，请重新读取用户")
				}
				identities = append(identities, UserIdentity{Source: d.Source, ExternalID: u.ID, Username: username})
			}
			d.Users[index].Bound = true
			continue
		}
		name := strings.TrimSpace(u.Name)
		if name == "" {
			name = u.Account
		}
		if len([]rune(name)) > 128 {
			return nil, fmt.Errorf("同步用户姓名过长")
		}
		now := time.Now().Unix()
		if action == "create" {
			_, err = tx.Exec(`INSERT INTO platform_users(username,display_name,role,password_hash,enabled,created_at,updated_at) VALUES(?,?,?,'',0,?,?)`, username, name, RoleMember, now, now)
			if err == nil {
				_, err = tx.Exec(`INSERT INTO platform_user_identities(source,external_id,username) VALUES(?,?,?)`, d.Source, u.ID, username)
			}
			users = append(users, User{Username: username})
			identities = append(identities, UserIdentity{Source: d.Source, ExternalID: u.ID, Username: username})
			d.Users[index].Bound = true
		} else {
			_, err = tx.Exec(`UPDATE platform_users SET display_name=?,updated_at=? WHERE username=?`, name, now, username)
		}
		if err != nil {
			return nil, fmt.Errorf("同步用户失败，未保存此次同步：%w", err)
		}
	}
	if apply {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

func (g *Gateway) fetchUserDirectory(ctx context.Context, connection string) (*UserDirectory, error) {
	g.mu.RLock()
	addr := ""
	for _, m := range g.manifests {
		if m.Name == "deploy" {
			addr = m.AdminAddr
			break
		}
	}
	g.mu.RUnlock()
	if addr == "" {
		return nil, fmt.Errorf("资源连接服务未启动")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/connections/"+url.PathEscape(connection)+"/user-directory", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Role", RoleAdmin)
	req.Header.Set("X-User", "platform-user-sync")
	client := &http.Client{Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法读取禅道用户，请检查资源服务与连接")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, fmt.Errorf("用户列表响应过大或读取失败")
	}
	if resp.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		if failure.Error != "" {
			return nil, fmt.Errorf("%s", failure.Error)
		}
		return nil, fmt.Errorf("资源服务返回 HTTP %d", resp.StatusCode)
	}
	var d UserDirectory
	if json.Unmarshal(raw, &d) != nil {
		return nil, fmt.Errorf("用户列表格式不兼容")
	}
	return &d, nil
}

func (g *Gateway) registerUserSync(mux *http.ServeMux, r *Registry) {
	for _, path := range []string{"/api/users/sync-preview", "/api/users/sync"} {
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, req *http.Request) {
			var body struct {
				Connection     string            `json:"connection"`
				IDs            []string          `json:"ids"`
				ExpectedSource string            `json:"expected_source"`
				Links          map[string]string `json:"links"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, req.Body, 128<<10)).Decode(&body) != nil || strings.TrimSpace(body.Connection) == "" {
				writeJSONStatus(w, 400, map[string]string{"error": "请选择禅道连接"})
				return
			}
			ctx, cancel := context.WithTimeout(req.Context(), 65*time.Second)
			defer cancel()
			// Always fetch authoritative records again on confirmation. Never
			// accept names, source identities, roles or passwords from the browser.
			d, err := g.fetchUserDirectory(ctx, body.Connection)
			if err == nil && req.URL.Path == "/api/users/sync" && body.ExpectedSource != d.Source {
				err = fmt.Errorf("用户来源已变化或未预览，请重新读取用户后同步")
			}
			if err == nil {
				d, err = r.SyncDirectory(*d, body.IDs, req.URL.Path == "/api/users/sync", body.Links)
			}
			if err != nil {
				writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, d)
		})
	}
}
