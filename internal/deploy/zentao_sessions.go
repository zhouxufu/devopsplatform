package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Cookies stay in memory, separated by connection and platform actor. Never
// share the read-only connection identity with a personal write session.
type zentaoSessionKey struct{ Actor, Connection string }
type zentaoSession struct {
	mu                   sync.Mutex
	fingerprint          string
	client               *zentaoLegacy
	lastUsed             time.Time
	requiresVerification bool
}

const zentaoSessionIdle = 30 * time.Minute

type zentaoAuthenticationError struct{ message string }

func (e *zentaoAuthenticationError) Error() string { return e.message }

func zentaoLoginRedirect(location string) bool {
	value := strings.ToLower(location)
	return strings.Contains(value, "user-login") || (strings.Contains(value, "m=user") && strings.Contains(value, "f=login"))
}
func (z *zentaoLegacy) expireAuthentication() error {
	z.authExpired.Store(true)
	if z.authInvalidated != nil {
		z.authInvalidated()
	}
	return &zentaoAuthenticationError{"来源认证已失效，请重新验证本人来源账号；未自动重试操作"}
}

func (s *Store) zentaoSession(key zentaoSessionKey) *zentaoSession {
	s.zentaoSessionsMu.Lock()
	defer s.zentaoSessionsMu.Unlock()
	if s.zentaoSessions == nil {
		s.zentaoSessions = map[zentaoSessionKey]*zentaoSession{}
	}
	if s.zentaoSessions[key] == nil {
		s.zentaoSessions[key] = &zentaoSession{}
	}
	return s.zentaoSessions[key]
}

func (s *Store) cachedZentao(ctx context.Context, c *ResourceConnection, actor, fingerprint string) (*zentaoLegacy, error) {
	entry := s.zentaoSession(zentaoSessionKey{actor, c.Name})
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.fingerprint != fingerprint {
		entry.client, entry.requiresVerification = nil, false
		entry.fingerprint = fingerprint
	}
	if entry.requiresVerification {
		if actor == "" {
			return nil, &zentaoAuthenticationError{"公共来源连接认证已失效，请管理员检查并更新连接凭据；未自动重试"}
		}
		return nil, &zentaoAuthenticationError{"来源认证已失效，请重新验证本人来源账号；未自动重试操作"}
	}
	if entry.client != nil && time.Since(entry.lastUsed) < zentaoSessionIdle {
		entry.lastUsed = time.Now()
		return entry.client, nil
	}
	z, err := newZentaoLegacy(ctx, c)
	if err != nil {
		var auth *zentaoAuthenticationError
		if errors.As(err, &auth) {
			entry.requiresVerification = true
		}
		return nil, err
	}
	s.installZentaoSession(entry, z, fingerprint)
	return z, nil
}

// Caller holds entry.mu. The callback only invalidates the exact session;
// transient network failures and object-level permission failures do not.
func (s *Store) installZentaoSession(entry *zentaoSession, z *zentaoLegacy, fingerprint string) {
	entry.client, entry.fingerprint, entry.lastUsed, entry.requiresVerification = z, fingerprint, time.Now(), false
	z.authInvalidated = func() {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.client == z {
			entry.requiresVerification = true
		}
	}
}

func (s *Store) forgetZentaoSession(actor, name string) {
	s.zentaoSessionsMu.Lock()
	entry := s.zentaoSessions[zentaoSessionKey{actor, name}]
	delete(s.zentaoSessions, zentaoSessionKey{actor, name})
	s.zentaoSessionsMu.Unlock()
	if entry != nil {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.client != nil {
			entry.client.authExpired.Store(true)
		}
		entry.client = nil
	}
}

func (s *Store) connectionZentao(ctx context.Context, c *ResourceConnection) (*zentaoLegacy, error) {
	// Source URL, credentials and config changes all invalidate connection cache.
	return s.cachedZentao(ctx, c, "", digestJSON([]any{c.Endpoint, c.Config, c.Username, c.Password}))
}

func (s *Store) personalZentaoCredentials(name, actor string) (*ResourceConnection, ResearchCredential, string, error) {
	c := s.GetConnection(name)
	if c == nil || c.Kind != "zentao" {
		return nil, ResearchCredential{}, "", fmt.Errorf("来源连接不存在")
	}
	var secret, endpoint, storedActor string
	if actor == "" || actor == "local" || s.db.QueryRow(`SELECT actor,secret,endpoint_digest FROM research_credentials WHERE actor=? AND connection_name=?`, actor, name).Scan(&storedActor, &secret, &endpoint) != nil || storedActor != actor || endpoint != digestJSON(c.Endpoint) {
		return nil, ResearchCredential{}, "", &zentaoAuthenticationError{"请先在来源账号设置中验证本人禅道账号；身份关联不等于授权，不能使用公共账号代操作"}
	}
	raw, err := s.decryptSecret(secret)
	var v ResearchCredential
	if err != nil || json.Unmarshal([]byte(raw), &v) != nil || v.Password == "" {
		return nil, v, "", &zentaoAuthenticationError{"个人凭据无法读取，请重新验证本人来源账号"}
	}
	return c, v, digestJSON([]any{c.Endpoint, c.Config, secret}), nil
}

func (s *Store) researchAuthStatus(name, actor string) (string, string) {
	_, v, fp, err := s.personalZentaoCredentials(name, actor)
	if err != nil {
		return "", "unverified"
	}
	entry := s.zentaoSession(zentaoSessionKey{actor, name})
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.fingerprint == fp && entry.requiresVerification {
		return v.Account, "needs_verification"
	}
	if entry.fingerprint == fp && entry.client != nil && time.Since(entry.lastUsed) < zentaoSessionIdle {
		return v.Account, "connected"
	}
	return v.Account, "saved"
}

// Called only on the account settings page, not every list/poll. Gateway strips
// caller-supplied identities and injects its own trusted identity directory.
func (s *Store) researchAccountHint(ctx context.Context, r *http.Request, name string) (string, error) {
	c := s.GetConnection(name)
	if c == nil || c.Kind != "zentao" {
		return "", fmt.Errorf("来源连接不存在")
	}
	var identities []struct {
		Source string `json:"source"`
		ID     string `json:"external_id"`
		User   string `json:"username"`
	}
	if json.Unmarshal([]byte(r.Header.Get("X-Research-Identities")), &identities) != nil {
		return "", nil
	}
	for _, identity := range identities {
		if identity.User != deliveryActor(r) || identity.Source != researchSourceIdentity(strings.TrimRight(c.Endpoint, "/")) {
			continue
		}
		directory, err := s.zentaoUsers(ctx, name)
		if err != nil {
			return "", err
		}
		for _, u := range directory.Users {
			if u.ID == identity.ID {
				return u.Account, nil
			}
		}
		return "", fmt.Errorf("已关联的来源人员不在当前目录中，请管理员核对人员关联")
	}
	return "", nil
}
