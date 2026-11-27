package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Read-only evidence, never an execution/approval token or runtime health claim.
type ResourceCheck struct {
	AffectedApplications []string `json:"affected_applications,omitempty"`
	Status               string   `json:"status"`
	Code                 string   `json:"code"`
	Message              string   `json:"message"`
	Suggestion           string   `json:"suggestion,omitempty"`
	CheckedAt            int64    `json:"checked_at,omitempty"`
	Actor                string   `json:"actor,omitempty"`
	Environment          string   `json:"environment,omitempty"`
	Alias                string   `json:"alias,omitempty"`
	Connection           string   `json:"connection,omitempty"`
	Kind                 string   `json:"kind,omitempty"`
	PersistenceWarning   string   `json:"persistence_warning,omitempty"`
}
type storedResourceCheck struct {
	Fingerprint string        `json:"fingerprint"`
	Check       ResourceCheck `json:"check"`
}

type resourceTransportError struct{ cause error }

func (e *resourceTransportError) Error() string {
	return "Nacos 网络连接失败或请求超时，请检查地址、TLS 和网络"
}
func (e *resourceTransportError) Unwrap() error { return e.cause }

type resourceRemoteError struct{ Code, Message, Suggestion string }

func (e *resourceRemoteError) Error() string { return e.Message }
func resourceFailure(err error) ResourceCheck {
	c := ResourceCheck{Status: "failed", Code: "connection_failed", Message: "连接或目标核验失败", Suggestion: "核对连接、认证及目标配置后重新核验"}
	var remote *resourceRemoteError
	if errors.As(err, &remote) {
		c.Code = remote.Code
		c.Message = remote.Message
		c.Suggestion = remote.Suggestion
		return c
	}
	if strings.HasPrefix(err.Error(), "WRONGPASS") || strings.HasPrefix(err.Error(), "NOAUTH") {
		c.Code = "authentication_failed"
		c.Message = "Redis 认证失败"
		return c
	}
	if strings.HasPrefix(err.Error(), "NOPERM") {
		c.Code = "permission_denied"
		c.Message = "Redis 账号权限不足"
		return c
	}
	var my *mysql.MySQLError
	switch {
	case errors.As(err, &my):
		switch my.Number {
		case 1045:
			c.Code = "authentication_failed"
			c.Message = "MySQL 认证失败（1045）"
			c.Suggestion = "核对账号、密码及平台服务器来源授权；不要覆盖其他应用的共享凭据"
		case 1049:
			c.Code = "database_missing"
			c.Message = "目标数据库不存在（1049）"
			c.Suggestion = "核对该环境的库名；平台不会自动创建数据库"
		case 1044, 1142, 1143:
			c.Code = "permission_denied"
			c.Message = "目标数据库或对象访问权限不足"
			c.Suggestion = "核对当前账号已授权范围；平台不会自动授予权限"
		}
	case errors.Is(err, syscall.ECONNREFUSED):
		c.Code = "connection_refused"
		c.Message = "目标端口拒绝连接"
		c.Suggestion = "核对服务是否监听、地址和端口，不需要反复修改密码"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		c.Code = "timeout"
		c.Message = "核验超时或已取消"
		c.Suggestion = "核对网络后手动重新核验，未执行任何业务写入"
	default:
		var dns *net.DNSError
		var network net.Error
		var cert x509.UnknownAuthorityError
		var host x509.HostnameError
		switch {
		case errors.As(err, &dns):
			c.Code = "dns_failed"
			c.Message = "目标地址解析失败"
			c.Suggestion = "核对平台服务器 DNS 和连接地址"
		case errors.As(err, &cert), errors.As(err, &host):
			c.Code = "tls_failed"
			c.Message = "TLS 证书核验失败"
			c.Suggestion = "配置正确证书和服务名称，不关闭证书核验"
		case errors.As(err, &network) && network.Timeout():
			c.Code = "timeout"
			c.Message = "连接超时"
			c.Suggestion = "核对网络连通性和服务状态"
		}
	}
	return c
}
func (s *Store) privateResourceDigest(value any) string {
	b, _ := json.Marshal(value)
	h := hmac.New(sha256.New, s.secretKey)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Store) connectionCheckFingerprint(c *ResourceConnection) string {
	if c == nil {
		return "missing"
	}
	return s.privateResourceDigest([]any{c.Name, c.Kind, c.Endpoint, c.Config, c.Username, c.Password, c.PrivateKey, c.Passphrase})
}
func (s *Store) readResourceCheck(key, fingerprint string) *ResourceCheck {
	var raw string
	var stored storedResourceCheck
	if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&raw) != nil || json.Unmarshal([]byte(raw), &stored) != nil {
		return nil
	}
	check := stored.Check
	if stored.Fingerprint != fingerprint {
		check.Status = "stale"
		check.Code = "configuration_changed"
		check.Message = "配置已变化，需重新核验"
		check.Suggestion = "原核验记录仍保留，但不能用于当前目标"
	}
	return &check
}
func (s *Store) saveResourceCheck(key, fingerprint string, check ResourceCheck) error {
	raw, err := json.Marshal(storedResourceCheck{Fingerprint: fingerprint, Check: check})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(raw))
	return err
}
func (s *Store) recordConnectionCheck(c *ResourceConnection, actor, message string, err error) (ResourceCheck, error) {
	check := ResourceCheck{Status: "passed", Code: "connection_verified", Message: message, CheckedAt: time.Now().Unix(), Actor: actor, Connection: c.Name, Kind: c.Kind}
	if err != nil {
		check = resourceFailure(err)
		check.CheckedAt = time.Now().Unix()
		check.Actor = actor
		check.Connection = c.Name
		check.Kind = c.Kind
		check.AffectedApplications = s.resourceConnectionImpact(c.Name)
		if len(check.AffectedApplications) > 0 {
			check.Suggestion += "；此连接被应用复用，可新建专用连接，避免直接覆盖共享凭据"
		}
	}
	e := s.saveResourceCheck("resource_connection_check:"+c.Name, s.connectionCheckFingerprint(c), check)
	return check, e
}
func (s *Store) bindingCheckFingerprint(b ApplicationEnvironmentResource) string {
	return s.privateResourceDigest([]any{b.Environment, b.Alias, b.Connection, b.Config, s.connectionCheckFingerprint(s.GetConnection(b.Connection))})
}
func resourceBindingCheckKey(service, environment, alias string) string {
	return "resource_binding_check:" + service + "/" + environment + "/" + alias
}
func (s *Store) attachApplicationResourceChecks(service string, value *ApplicationResources) {
	value.Checks = []ResourceCheck{}
	for _, b := range value.Bindings {
		if check := s.readResourceCheck(resourceBindingCheckKey(service, b.Environment, b.Alias), s.bindingCheckFingerprint(b)); check != nil {
			value.Checks = append(value.Checks, *check)
		}
	}
}
func (s *Store) probeResourceBinding(ctx context.Context, kind string, b ApplicationEnvironmentResource) ResourceCheck {
	c := s.GetConnection(b.Connection)
	if c == nil {
		return ResourceCheck{Status: "failed", Code: "connection_missing", Message: "所选连接不存在", Suggestion: "重新选择该环境的资源连接"}
	}
	var err error
	message := ""
	switch kind {
	case "mysql":
		var target *ResourceConnection
		target, err = mysqlDatabaseTarget(c, b.Config)
		if err == nil {
			var actual string
			db, e := openChangeMySQL(ctx, target)
			err = e
			if err == nil {
				err = db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&actual)
				db.Close()
				if err == nil && actual != target.Config["database"] {
					err = fmt.Errorf("database identity mismatch")
				}
			}
		}
		message = "目标库存在且可连接；未验证表级读写权限"
	case "nacos":
		var client *nacosClient
		client, err = newNacosClient(ctx, c)
		if err == nil {
			_, err = client.read(ctx, b.Config["data_id"], b.Config["group"])
		}
		message = "目标配置读取通过；未验证发布权限，未发布配置"
	case "redis":
		err = testIntegrationConnection(ctx, c)
		message = "认证、DB 选择和 PING 通过；未验证维护权限，未执行写操作"
	case xxlJobKind:
		err = validateXXLBinding(c, b)
		if err == nil {
			_, err = (xxlJobBinding{Connection: c, AppName: b.Config["app_name"]}).group(ctx)
		}
		message = "目标执行器可读取，未触发任务"
	default:
		return ResourceCheck{Status: "unsupported", Code: "unsupported_kind", Message: "此资源类型尚不支持独立核验"}
	}
	if err != nil {
		return resourceFailure(err)
	}
	return ResourceCheck{Status: "passed", Code: "target_verified", Message: message}
}

type resourceCheckRequest struct {
	Resources    *ApplicationResources `json:"resources,omitempty"`
	Environments []string              `json:"environments"`
	Aliases      []string              `json:"aliases,omitempty"`
}

func (s *Store) checkApplicationResources(ctx context.Context, service, actor string, in ApplicationResources, environments, aliases []string, probe func(context.Context, string, ApplicationEnvironmentResource) ResourceCheck) ([]ResourceCheck, error) {
	if len(environments) == 0 || len(environments) > 10 {
		return nil, fmt.Errorf("请显式选择要核验的环境，最多 10 个；不会自动检查其他环境")
	}
	envSet := map[string]bool{}
	aliasSet := map[string]bool{}
	uniqueEnvironments := []string{}
	for _, env := range environments {
		if envSet[env] {
			continue
		}
		uniqueEnvironments = append(uniqueEnvironments, env)
		if s.GetEnvironment(env) == nil {
			return nil, fmt.Errorf("核验环境不存在")
		}
		envSet[env] = true
	}
	for _, alias := range aliases {
		aliasSet[alias] = true
	}
	if in.CompositionVersion == 2 {
		var err error
		in, err = s.composeResources(service, in)
		if err != nil {
			return nil, err
		}
	} else if _, err := s.normalizeApplicationResources(service, &in); err != nil {
		return nil, err
	}
	type item struct {
		r ApplicationResourceRequirement
		b *ApplicationEnvironmentResource
	}
	items := []item{}
	for _, r := range in.Requirements {
		if len(aliasSet) > 0 && !aliasSet[r.Alias] {
			continue
		}
		for _, env := range uniqueEnvironments {
			var binding *ApplicationEnvironmentResource
			for _, b := range in.Bindings {
				if b.Alias == r.Alias && b.Environment == env {
					copy := b
					binding = &copy
					break
				}
			}
			if binding == nil {
				binding = &ApplicationEnvironmentResource{Environment: env, Alias: r.Alias}
			}
			items = append(items, item{r, binding})
		}
	}
	if len(items) > 100 {
		return nil, fmt.Errorf("每次最多核验 100 项，请按环境或类型缩小范围")
	}
	output := make([]ResourceCheck, len(items))
	var next int
	var mu sync.Mutex
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for {
			mu.Lock()
			at := next
			next++
			mu.Unlock()
			if at >= len(items) {
				return
			}
			it := items[at]
			b := *it.b
			check := unboundResourceCheck(it.r, b.Environment)
			fingerprint := s.bindingCheckFingerprint(b)
			if b.Connection != "" {
				sampleCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
				check = probe(sampleCtx, it.r.Kind, b)
				cancel()
			}
			check.Environment = b.Environment
			check.Alias = b.Alias
			check.Connection = b.Connection
			check.Kind = it.r.Kind
			check.Actor = actor
			check.CheckedAt = time.Now().Unix()
			if b.Connection != "" {
				current := s.ApplicationResources(service)
				for _, saved := range current.Bindings {
					if saved.Environment == b.Environment && saved.Alias == b.Alias && s.bindingCheckFingerprint(saved) == fingerprint {
						if err := s.saveResourceCheck(resourceBindingCheckKey(service, b.Environment, b.Alias), fingerprint, check); err != nil {
							check.PersistenceWarning = "本次结果未能持久保存，请稍后重新核验"
						}
						break
					}
				}
			}
			output[at] = check
		}
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go worker()
	}
	wg.Wait()
	return output, nil
}
func registerResourceCheckRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /api/services/{name}/resources/check", func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "资源核验需要管理员权限")
			return
		}
		service := r.PathValue("name")
		if !s.hasResourceApplication(service) {
			deliveryWriteError(w, 404, "应用不存在")
			return
		}
		var req resourceCheckRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "核验参数无效")
			return
		}
		value := s.ApplicationResources(service)
		if req.Resources != nil {
			value = *req.Resources
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		checks, err := s.checkApplicationResources(ctx, service, deliveryActor(r), value, req.Environments, req.Aliases, s.probeResourceBinding)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		counts := map[string]int{}
		for _, check := range checks {
			counts[check.Status]++
		}
		deliveryJSON(w, map[string]any{"items": checks, "counts": counts, "permission_verified": false})
	})
}
