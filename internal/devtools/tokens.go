package devtools

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const tokenPrefix = "lp_dev_"

var allowedScopes = map[string]bool{
	"resources:read":   true,
	"resources:write":  true,
	"apitests:read":    true,
	"apitests:write":   true,
	"apitests:execute": true,
	"apidocs:read":     true,
	"apidocs:write":    true,
	"arthas:read":      true,
	"logs:read":        true,
	"metrics:read":     true,
	"traces:read":      true,
	"builds:read":      true,
	"releases:read":    true,
	"workitems:read":   true,
	"workitems:write":  true,
	"changes:read":     true,
	"changes:write":    true,
	"builds:trigger":   true,
	"delivery:read":    true,
	"delivery:write":   true,
	"delivery:prepare": true,
	"delivery:execute": true,
}

func adminTokenScope(scope string) bool {
	switch scope {
	case "resources:read", "resources:write", "apidocs:write", "arthas:read":
		return true
	default:
		return false
	}
}

type AccessToken struct {
	Owner        string `json:"owner,omitempty"`
	role         string
	verified     bool
	identities   string
	displayNames string
	reviewers    string
	Revision     int64    `json:"revision"`
	DeletedAt    int64    `json:"deleted_at,omitempty"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Prefix       string   `json:"prefix"`
	Hash         string   `json:"hash,omitempty"`
	Scopes       []string `json:"scopes"`
	Applications []string `json:"applications"`
	Environments []string `json:"environments"`
	CreatedAt    int64    `json:"created_at"`
	ExpiresAt    int64    `json:"expires_at"`
	LastUsedAt   int64    `json:"last_used_at,omitempty"`
	Revoked      bool     `json:"revoked"`
}

type AuditEvent struct {
	Actor       string `json:"actor,omitempty"`
	ID          int64  `json:"id"`
	TokenID     string `json:"token_id"`
	TokenName   string `json:"token_name"`
	Tool        string `json:"tool"`
	Service     string `json:"service,omitempty"`
	Environment string `json:"environment,omitempty"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	CreatedAt   int64  `json:"created_at"`
}

type tokenFile struct {
	Actions []PlatformAction `json:"actions,omitempty"`
	Tokens  []AccessToken    `json:"tokens"`
	Audit   []AuditEvent     `json:"audit"`
	NextID  int64            `json:"next_audit_id"`
}

type TokenStore struct {
	mu   sync.Mutex
	path string
	data tokenFile
}

func OpenTokenStore(dir string) (*TokenStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &TokenStore{path: filepath.Join(dir, "devtools-tokens.json")}
	raw, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("读取开发工具令牌: %w", err)
		}
	}
	return s, nil
}

func (s *TokenStore) saveLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type CreateTokenRequest struct {
	Owner         string   `json:"-"`
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	Applications  []string `json:"applications"`
	Environments  []string `json:"environments"`
	ExpiresInDays int      `json:"expires_in_days"`
}

func normalizeValues(values []string, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) == max {
			break
		}
	}
	sort.Strings(out)
	return out
}

func validateScopeValues(label string, values []string) error {
	for _, value := range values {
		if len([]rune(value)) > 128 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s范围包含无效值", label)
		}
	}
	return nil
}

func validateTokenRequest(req CreateTokenRequest) (CreateTokenRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 64 {
		return req, fmt.Errorf("令牌名称必填且不能超过 64 个字符")
	}
	req.Scopes = normalizeValues(req.Scopes, len(allowedScopes))
	if len(req.Scopes) == 0 {
		return req, fmt.Errorf("至少选择一个权限")
	}
	for _, scope := range req.Scopes {
		if !allowedScopes[scope] {
			return req, fmt.Errorf("不支持的权限 %s", scope)
		}
	}
	if req.Owner == "local" {
		req.Owner = ""
	}
	for _, scope := range req.Scopes {
		if (isWriteScope(scope) || scope == "arthas:read") && (req.Owner == "" || req.Owner == "local") {
			return req, fmt.Errorf("写权限令牌必须由已登录的实名平台账号创建")
		}
	}
	req.Applications = normalizeValues(req.Applications, 100)
	req.Environments = normalizeValues(req.Environments, 20)
	if len(req.Applications) == 0 || len(req.Environments) == 0 {
		return req, fmt.Errorf("应用和环境范围必填；需要全部范围时显式填写 *")
	}
	if err := validateScopeValues("应用", req.Applications); err != nil {
		return req, err
	}
	if err := validateScopeValues("环境", req.Environments); err != nil {
		return req, err
	}
	if req.ExpiresInDays <= 0 {
		req.ExpiresInDays = 30
	}
	if req.ExpiresInDays > 365 {
		return req, fmt.Errorf("有效期不能超过 365 天")
	}
	return req, nil
}

func (s *TokenStore) Create(req CreateTokenRequest) (AccessToken, string, error) {
	var validationErr error
	req, validationErr = validateTokenRequest(req)
	if validationErr != nil {
		return AccessToken{}, "", validationErr
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return AccessToken{}, "", err
	}
	rawToken := tokenPrefix + hex.EncodeToString(random)
	hash := sha256.Sum256([]byte(rawToken))
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return AccessToken{}, "", err
	}
	now := time.Now()
	token := AccessToken{
		Owner: req.Owner,
		ID:    hex.EncodeToString(idBytes), Name: req.Name, Prefix: rawToken[:15], Hash: hex.EncodeToString(hash[:]),
		Scopes: req.Scopes, Applications: req.Applications, Environments: req.Environments,
		CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).Unix(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Tokens = append(s.data.Tokens, token)
	if err := s.saveLocked(); err != nil {
		return AccessToken{}, "", err
	}
	return token, rawToken, nil
}

func (s *TokenStore) List() []AccessToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []AccessToken{}
	for _, t := range s.data.Tokens {
		if t.DeletedAt == 0 {
			out = append(out, t)
		}
	}
	for i := range out {
		out[i].Hash = ""
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (s *TokenStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Tokens {
		if s.data.Tokens[i].ID == id {
			s.data.Tokens[i].Revoked = true
			s.data.Tokens[i].Revision++
			return s.saveLocked()
		}
	}
	return fmt.Errorf("令牌不存在")
}

func (s *TokenStore) Authenticate(raw string) (*AccessToken, error) {
	if !strings.HasPrefix(raw, tokenPrefix) || len(raw) > 256 {
		return nil, fmt.Errorf("无效的开发工具令牌")
	}
	hash := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(hash[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for i := range s.data.Tokens {
		token := &s.data.Tokens[i]
		if subtle.ConstantTimeCompare([]byte(token.Hash), []byte(want)) != 1 {
			continue
		}
		if token.Revoked || token.DeletedAt > 0 {
			return nil, fmt.Errorf("开发工具令牌已撤销")
		}
		if token.ExpiresAt <= now {
			return nil, fmt.Errorf("开发工具令牌已过期")
		}
		copyToken := *token
		copyToken.Scopes = append([]string(nil), token.Scopes...)
		copyToken.Applications = append([]string(nil), token.Applications...)
		copyToken.Environments = append([]string(nil), token.Environments...)
		if now-token.LastUsedAt >= 60 {
			token.LastUsedAt = now
			_ = s.saveLocked()
		}
		return &copyToken, nil
	}
	return nil, fmt.Errorf("无效的开发工具令牌")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == "*" || value == target {
			return true
		}
	}
	return false
}

func (t *AccessToken) HasScope(scope string) bool { return contains(t.Scopes, scope) }

func (t *AccessToken) Allows(service, environment string) error {
	if service != "" && !contains(t.Applications, service) {
		return fmt.Errorf("令牌无权访问应用 %s", service)
	}
	if environment != "" && !contains(t.Environments, environment) {
		return fmt.Errorf("令牌无权访问环境 %s", environment)
	}
	return nil
}

func (s *TokenStore) Audit(token *AccessToken, tool, service, environment string, started time.Time, callErr error) {
	e := AuditEvent{TokenID: token.ID, TokenName: token.Name, Tool: tool, Service: service, Environment: environment, Success: callErr == nil, DurationMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().Unix()}
	e.Actor = token.Owner
	if callErr != nil {
		e.Error = scrubString(callErr.Error())
		if len(e.Error) > 300 {
			e.Error = e.Error[:300]
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.NextID++
	e.ID = s.data.NextID
	s.data.Audit = append(s.data.Audit, e)
	if len(s.data.Audit) > 1000 {
		s.data.Audit = append([]AuditEvent(nil), s.data.Audit[len(s.data.Audit)-1000:]...)
	}
	_ = s.saveLocked()
}

func (s *TokenStore) AuditEvents(limit int) []AuditEvent {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.data.Audit)
	if count > limit {
		count = limit
	}
	out := make([]AuditEvent, 0, count)
	for i := len(s.data.Audit) - 1; i >= 0 && len(out) < count; i-- {
		out = append(out, s.data.Audit[i])
	}
	return out
}

// Edits retain the original secret and owner. Old previews are invalidated on
// authorization changes, even when the same key remains in the client.
type UpdateTokenRequest struct {
	Revision      *int64   `json:"revision"`
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	Applications  []string `json:"applications"`
	Environments  []string `json:"environments"`
	ExpiresInDays int      `json:"expires_in_days"` // zero preserves existing expiry
}

func (s *TokenStore) maintainToken(id, actor string, admin bool, req *UpdateTokenRequest, remove bool) (AccessToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Tokens {
		old := s.data.Tokens[i]
		if old.ID != id || old.DeletedAt > 0 {
			continue
		}
		if actor == "" || actor == "local" || (!admin && old.Owner != actor) {
			return AccessToken{}, fmt.Errorf("无权维护此令牌")
		}
		if req == nil || req.Revision == nil || *req.Revision != old.Revision {
			return AccessToken{}, fmt.Errorf("令牌已变化，请刷新后重新编辑")
		}
		for _, a := range s.data.Actions {
			if a.TokenID == id && a.State == "executing" {
				return AccessToken{}, fmt.Errorf("令牌有执行中操作，先核对原操作结果")
			}
		}
		next := old
		if remove {
			next.DeletedAt = time.Now().Unix()
			next.Revoked = true
		} else {
			if old.Revoked || old.ExpiresAt <= time.Now().Unix() {
				return AccessToken{}, fmt.Errorf("撤销/过期令牌不可恢复，请创建新令牌")
			}
			normalized, err := validateTokenRequest(CreateTokenRequest{Owner: old.Owner, Name: req.Name, Scopes: req.Scopes, Applications: req.Applications, Environments: req.Environments, ExpiresInDays: req.ExpiresInDays})
			if err != nil {
				return AccessToken{}, err
			}
			for _, scope := range normalized.Scopes {
				if !admin && adminTokenScope(scope) && !contains(old.Scopes, scope) {
					return AccessToken{}, fmt.Errorf("此权限仅管理员可授权")
				}
			}
			next.Name, next.Scopes, next.Applications, next.Environments = normalized.Name, normalized.Scopes, normalized.Applications, normalized.Environments
			if req.ExpiresInDays < 0 {
				return AccessToken{}, fmt.Errorf("有效期无效")
			}
			if req.ExpiresInDays > 0 {
				next.ExpiresAt = time.Now().Add(time.Duration(normalized.ExpiresInDays) * 24 * time.Hour).Unix()
			}
		}
		next.Revision++
		// Snapshot for an atomic persistence failure. Audit never contains secret/hash.
		previous := s.data
		previous.Tokens = append([]AccessToken(nil), s.data.Tokens...)
		previous.Actions = append([]PlatformAction(nil), s.data.Actions...)
		previous.Audit = append([]AuditEvent(nil), s.data.Audit...)
		s.data.Tokens[i] = next
		for j := range s.data.Actions {
			if s.data.Actions[j].TokenID == id && s.data.Actions[j].State == "preview" {
				s.data.Actions[j].State = "stale"
				s.data.Actions[j].Error = "访问令牌已修改或删除，请重新预览"
			}
		}
		action := "update_token"
		if remove {
			action = "delete_token"
		}
		s.data.NextID++
		s.data.Audit = append(s.data.Audit, AuditEvent{ID: s.data.NextID, Actor: actor, TokenID: id, TokenName: next.Name, Tool: action, Success: true, CreatedAt: time.Now().Unix()})
		if err := s.saveLocked(); err != nil {
			s.data = previous
			return AccessToken{}, err
		}
		next.Hash = ""
		return next, nil
	}
	return AccessToken{}, fmt.Errorf("令牌不存在")
}

func (s *TokenStore) auditTokenIDs(actor string, admin bool) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, t := range s.data.Tokens {
		if admin || t.Owner == actor && actor != "" {
			out[t.ID] = true
		}
	}
	return out
}
