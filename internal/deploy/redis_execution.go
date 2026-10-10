package deploy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// Commands are Redis argv, never shell scripts. No pattern expansion, Lua,
// transactions, redirection or implicit retry is allowed by this executor.
type RedisChange struct {
	ResourceAlias string `json:"resource_alias"`
	Commands      string `json:"commands"`
}
type RedisChangePlan struct {
	ChangeID          int64      `json:"change_id"`
	Service           string     `json:"service"`
	Alias             string     `json:"alias"`
	Connection        string     `json:"connection"`
	Endpoint          string     `json:"endpoint"`
	DB                string     `json:"db"`
	Prefix            string     `json:"prefix"`
	DestinationDigest string     `json:"destination_digest"`
	ChangeDigest      string     `json:"change_digest"`
	Commands          [][]string `json:"commands"`
}
type RedisExecutionPlan struct {
	NodeID  string            `json:"node_id"`
	Changes []RedisChangePlan `json:"changes"`
}

func validateRedisConnection(c *ResourceConnection) error {
	host, port, err := net.SplitHostPort(c.Endpoint)
	p, pe := strconv.Atoi(port)
	if err != nil || pe != nil || p < 1 || p > 65535 || host == "" || strings.ContainsAny(host, " /@?#") {
		return fmt.Errorf("Redis 地址填写 host:port，不含账号密码或 redis://")
	}
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if c.Config["db"] == "" {
		c.Config["db"] = "0"
	}
	db, err := strconv.Atoi(c.Config["db"])
	if err != nil || db < 0 || db > 15 {
		return fmt.Errorf("Redis DB 为 0–15；云代理通常只支持 DB 0")
	}
	c.Config["db"] = strconv.Itoa(db)
	if c.Config["tls"] == "" {
		c.Config["tls"] = "false"
	}
	if c.Config["tls"] != "true" && c.Config["tls"] != "false" {
		return fmt.Errorf("Redis TLS 仅支持 true 或 false，不能跳过证书校验")
	}
	if c.Config["redis_auth"] == "" {
		c.Config["redis_auth"] = "password"
	}
	if c.Config["redis_auth"] != "password" && c.Config["redis_auth"] != "none" {
		return fmt.Errorf("Redis 认证支持 password 或 none")
	}
	prefix := c.Config["key_prefix"]
	if prefix == "" || len(prefix) > 200 || strings.ContainsAny(prefix, "*?[]") || strings.IndexFunc(prefix, unicode.IsControl) >= 0 {
		return fmt.Errorf("Redis 必须配置非空精确 Key 前缀（如 menu:），不允许通配符")
	}
	return nil
}
func newRedisClient(ctx context.Context, c *ResourceConnection, bounded ...bool) (*redis.Client, error) {
	if c == nil || c.Kind != "redis" {
		return nil, fmt.Errorf("Redis 连接不存在")
	}
	if err := validateRedisConnection(c); err != nil {
		return nil, err
	}
	db, _ := strconv.Atoi(c.Config["db"])
	opts := &redis.Options{Addr: c.Endpoint, DB: db, Protocol: 2, MaxRetries: -1, PoolSize: 1, DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, ContextTimeoutEnabled: true, DisableIndentity: true}
	if c.Config["redis_auth"] == "password" {
		if c.Password == "" {
			return nil, fmt.Errorf("请配置 Redis 密码；ACL 用户名可留空")
		}
		opts.Username, opts.Password = c.Username, c.Password
	}
	if c.Config["tls"] == "true" {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if len(bounded) > 0 && bounded[0] {
		opts.Dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 5 * time.Second}
			var conn net.Conn
			var err error
			if opts.TLSConfig != nil {
				host, _, e := net.SplitHostPort(addr)
				if e != nil {
					return nil, e
				}
				cfg := opts.TLSConfig.Clone()
				cfg.ServerName = host
				conn, err = (&tls.Dialer{NetDialer: d, Config: cfg}).DialContext(ctx, network, addr)
			} else {
				conn, err = d.DialContext(ctx, network, addr)
			}
			if err != nil {
				return nil, err
			}
			return &boundedRESPConn{Conn: conn}, nil
		}
	}
	client := redis.NewClient(opts)
	if pong, err := client.Ping(ctx).Result(); err != nil || pong != "PONG" {
		client.Close()
		return nil, fmt.Errorf("Redis 连接检查失败：%s", redisErrorCategory(err))
	}
	return client, nil
}

// Never echo an upstream reply (it can contain keys, values or credentials).
func redisErrorCategory(err error) string {
	var oversized *redisResponseLimitError
	if errors.As(err, &oversized) {
		return oversized.message
	}
	if err == nil {
		return "服务未返回 PONG，请确认端口为 Redis 协议"
	}
	for _, item := range []struct{ prefix, message string }{
		{"NOAUTH", "需要认证，请配置密码"}, {"WRONGPASS", "账号或密码错误"}, {"NOPERM", "ACL 无该命令权限，请配置最小权限账号"},
		{"WRONGTYPE", "Key 类型与命令不匹配"}, {"MOVED", "原生 Cluster 重定向尚不支持，请使用标准代理地址"}, {"ASK", "原生 Cluster 重定向尚不支持，请使用标准代理地址"},
	} {
		if redis.HasErrorPrefix(err, item.prefix) {
			return item.message
		}
	}
	if _, ok := err.(redis.Error); ok {
		return "服务拒绝命令，请核对 DB、命令支持版本和代理限制"
	}
	return "网络、TLS 或连接中断，请核对证书、白名单与地址；已提交命令须人工核对结果"
}

func parseRedisCommands(text string) ([][]string, error) {
	return parseRedisCommandText(text, redisCommandKeys)
}
func parseRedisCommandText(text string, keys func([]string) ([]string, error)) ([][]string, error) {
	if len(text) > 32768 {
		return nil, fmt.Errorf("Redis 命令文本最多 32 KiB")
	}
	commands := [][]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		args := []string{}
		for i := 0; i < len(line); {
			if line[i] == ' ' || line[i] == '\t' {
				i++
				continue
			}
			var token strings.Builder
			for i < len(line) && line[i] != ' ' && line[i] != '\t' {
				if line[i] == '\'' || line[i] == '"' {
					quote := line[i]
					i++
					closed := false
					for i < len(line) {
						if line[i] == quote {
							i++
							closed = true
							break
						}
						if line[i] == '\\' && quote == '"' {
							i++
							if i >= len(line) {
								break
							}
						}
						token.WriteByte(line[i])
						i++
					}
					if !closed {
						return nil, fmt.Errorf("Redis 命令引号未闭合")
					}
				} else {
					if strings.ContainsRune(";|&<>`$\\", rune(line[i])) {
						return nil, fmt.Errorf("Redis 不支持 shell 表达式、管道或转义；空格参数请使用引号")
					}
					token.WriteByte(line[i])
					i++
				}
			}
			arg := token.String()
			if len(arg) > 1024 || strings.IndexFunc(arg, unicode.IsControl) >= 0 {
				return nil, fmt.Errorf("命令参数最多 1024 字节，不能含控制字符")
			}
			args = append(args, arg)
		}
		if len(args) == 0 {
			continue
		}
		args[0] = strings.ToUpper(args[0])
		if _, err := keys(args); err != nil {
			return nil, err
		}
		commands = append(commands, args)
	}
	if len(commands) == 0 || len(commands) > 50 {
		return nil, fmt.Errorf("每项 Redis 变更需要 1–50 条命令")
	}
	return commands, nil
}
func redisCommandKeys(a []string) ([]string, error) {
	if len(a) == 0 {
		return nil, fmt.Errorf("Redis 命令为空")
	}
	bad := func() ([]string, error) {
		return nil, fmt.Errorf("Redis 命令 %s 的参数无效或数量超限", a[0])
	}
	switch a[0] {
	case "PING":
		if len(a) != 1 {
			return bad()
		}
		return nil, nil
	case "TYPE", "TTL", "PTTL", "STRLEN", "HLEN", "LLEN", "SCARD", "ZCARD", "PERSIST":
		if len(a) != 2 {
			return bad()
		}
	case "EXPIRE", "PEXPIRE":
		if len(a) != 3 {
			return bad()
		}
		n, e := strconv.ParseInt(a[2], 10, 64)
		max := int64(31536000)
		if a[0] == "PEXPIRE" {
			max *= 1000
		}
		if e != nil || n < 1 || n > max {
			return bad()
		}
	case "EXISTS", "UNLINK":
		if len(a) < 2 || len(a) > 51 {
			return bad()
		}
		return a[1:], nil
	case "HDEL":
		if len(a) < 3 || len(a) > 52 {
			return bad()
		}
	default:
		return nil, fmt.Errorf("命令 %s 不在安全白名单；删除请用 UNLINK，禁止 KEYS/FLUSH*/EVAL/CONFIG/SELECT/SET 等", a[0])
	}
	return []string{a[1]}, nil
}
func normalizeRedisChange(c *Change) error {
	if c.Kind != "redis" || c.Service == "" || !validResourceName(c.Redis.ResourceAlias) {
		return fmt.Errorf("Redis 命令必须关联应用与资源别名")
	}
	_, err := parseRedisCommands(c.Redis.Commands)
	return err
}
func (s *Store) redisBinding(service, environment, alias string) (*ResourceConnection, error) {
	if !s.hasResourceApplication(service) || s.GetEnvironment(environment) == nil {
		return nil, fmt.Errorf("应用或环境不存在")
	}
	r := s.ApplicationResources(service)
	found := false
	for _, req := range r.Requirements {
		if req.Alias == alias && req.Kind == "redis" {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("应用未声明 Redis 资源 %s", alias)
	}
	for _, b := range r.Bindings {
		if b.Environment == environment && b.Alias == alias {
			c := s.GetConnection(b.Connection)
			if c == nil || c.Kind != "redis" || len(b.Config) > 0 {
				return nil, fmt.Errorf("Redis 绑定无效；DB 和前缀在连接中配置，不允许绑定覆盖")
			}
			return c, nil
		}
	}
	return nil, fmt.Errorf("Redis 资源 %s 未绑定 %s 环境", alias, environment)
}
func redisTargetKey(p RedisChangePlan) string { return digestJSON([]string{p.Endpoint, p.DB}) }
func (s *Store) prepareRedisNode(ctx context.Context, nodeID string, ids []int64, environment string) (*RedisExecutionPlan, error) {
	if len(ids) == 0 || len(ids) > 20 {
		return nil, fmt.Errorf("Redis 节点选择 1–20 项变更")
	}
	p := &RedisExecutionPlan{NodeID: nodeID, Changes: []RedisChangePlan{}}
	for _, id := range ids {
		change := s.GetChange(id)
		if change == nil || change.Redis == nil {
			return nil, fmt.Errorf("Redis 变更 #%d 缺少结构化命令，请重新编辑", id)
		}
		if err := normalizeRedisChange(change); err != nil {
			return nil, err
		}
		c, err := s.redisBinding(change.Service, environment, change.Redis.ResourceAlias)
		if err != nil {
			return nil, err
		}
		commands, err := parseRedisCommands(change.Redis.Commands)
		if err != nil {
			return nil, err
		}
		keys := 0
		for _, a := range commands {
			ks, _ := redisCommandKeys(a)
			keys += len(ks)
			for _, k := range ks {
				if k == c.Config["key_prefix"] || !strings.HasPrefix(k, c.Config["key_prefix"]) || strings.ContainsAny(k, "*?[]") {
					return nil, fmt.Errorf("所有 Key 必须在前缀 %s 内且为精确 Key；不允许通配符或前缀本身", c.Config["key_prefix"])
				}
			}
		}
		if keys > 200 {
			return nil, fmt.Errorf("每项变更最多指定 200 个 Key 参数")
		}
		client, err := newRedisClient(ctx, c)
		if err != nil {
			return nil, err
		}
		client.Close() // preview only authenticates/selects/PINGs; no maintenance commands.
		item := RedisChangePlan{ChangeID: id, Service: change.Service, Alias: change.Redis.ResourceAlias, Connection: c.Name, Endpoint: c.Endpoint, DB: c.Config["db"], Prefix: c.Config["key_prefix"], DestinationDigest: sqlDestination(c), ChangeDigest: digestJSON([]any{change.Service, change.Redis}), Commands: commands}
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM redis_execution_ledger WHERE target_key=? AND change_id=?`, redisTargetKey(item), id).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, fmt.Errorf("Redis 变更 #%d 已执行或结果待核对，不能重试", id)
		}
		p.Changes = append(p.Changes, item)
	}
	return p, nil
}
func (s *Store) executeRedisNode(ctx context.Context, run *DeliveryRun, p *RedisExecutionPlan) (string, error) {
	clients := []*redis.Client{}
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for _, item := range p.Changes {
		change := s.GetChange(item.ChangeID)
		if change == nil || change.Status != ChangeReady || digestJSON([]any{change.Service, change.Redis}) != item.ChangeDigest {
			return "", fmt.Errorf("Redis 变更内容或状态变化，请重新审核")
		}
		c, err := s.redisBinding(item.Service, run.Plan.Environment.Name, item.Alias)
		if err != nil || c.Name != item.Connection || sqlDestination(c) != item.DestinationDigest {
			return "", fmt.Errorf("Redis 连接或环境绑定变化，请重新审核")
		}
		client, err := newRedisClient(ctx, c)
		if err != nil {
			return "", err
		}
		clients = append(clients, client)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for _, item := range p.Changes {
		for i := range item.Commands {
			_, err = tx.Exec(`INSERT INTO redis_execution_ledger(target_key,change_id,command_index,run_id,node_id,status,result) VALUES(?,?,?,?,?,'reserved','尚未开始；执行中断后须核对台账')`, redisTargetKey(item), item.ChangeID, i, run.ID, p.NodeID)
			if err != nil {
				return "", fmt.Errorf("Redis 执行权已被保留，禁止重复执行")
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	for j, item := range p.Changes {
		for i, args := range item.Commands {
			update := func(status, result string) error {
				_, e := s.db.Exec(`UPDATE redis_execution_ledger SET status=?,result=? WHERE target_key=? AND change_id=? AND command_index=? AND run_id=?`, status, result, redisTargetKey(item), item.ChangeID, i, run.ID)
				return e
			}
			if err = update("executing", "命令已保留执行权，异常时结果待核对"); err != nil {
				return "", err
			}
			argv := make([]any, len(args))
			for k, a := range args {
				argv[k] = a
			}
			value, e := clients[j].Do(ctx, argv...).Result()
			if e != nil {
				message := redisErrorCategory(e)
				_ = update("uncertain", message+"；须核对，不自动重试")
				return fmt.Sprintf("Redis 命令 #%d：%s；后续命令未执行，不自动重试", i+1, message), fmt.Errorf("Redis 命令失败，结果须核对")
			}
			result := ""
			switch v := value.(type) {
			case int64:
				result = strconv.FormatInt(v, 10)
			case string:
				if args[0] == "PING" && v == "PONG" {
					result = v
				} else if args[0] == "TYPE" && map[string]bool{"none": true, "string": true, "hash": true, "list": true, "set": true, "zset": true, "stream": true}[v] {
					result = v
				}
			}
			if result == "" {
				_ = update("uncertain", "服务返回了非预期响应，需核对")
				return "", fmt.Errorf("Redis 返回非预期响应，停止后续命令")
			}
			if err = update("success", result); err != nil {
				return "", fmt.Errorf("命令已提交但台账保存失败，禁止重试，请核对远端")
			}
		}
	}
	return "Redis 命令顺序执行完成；整数结果为影响数量或状态，不代表业务数据验证", nil
}

type RedisLedgerEntry struct {
	ChangeID     int64  `json:"change_id"`
	CommandIndex int    `json:"command_index"`
	RunID        int64  `json:"run_id"`
	NodeID       string `json:"node_id"`
	Status       string `json:"status"`
	Result       string `json:"result"`
}

func registerRedisRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/redis-executions", func(w http.ResponseWriter, r *http.Request) {
		q := `SELECT change_id,command_index,run_id,node_id,status,result FROM redis_execution_ledger`
		args := []any{}
		if raw := r.URL.Query().Get("run_id"); raw != "" {
			id, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || id < 1 {
				writeIntegrationJSON(w, nil, fmt.Errorf("执行单 ID 无效"))
				return
			}
			q += ` WHERE run_id=?`
			args = append(args, id)
		}
		q += ` ORDER BY run_id DESC,change_id,command_index LIMIT 1000`
		rows, e := s.db.Query(q, args...)
		if e != nil {
			writeIntegrationJSON(w, nil, e)
			return
		}
		defer rows.Close()
		out := []RedisLedgerEntry{}
		for rows.Next() {
			var item RedisLedgerEntry
			if e = rows.Scan(&item.ChangeID, &item.CommandIndex, &item.RunID, &item.NodeID, &item.Status, &item.Result); e != nil {
				writeIntegrationJSON(w, nil, e)
				return
			}
			out = append(out, item)
		}
		writeIntegrationJSON(w, out, rows.Err())
	})
	mux.HandleFunc("POST /api/changes/{id}/redis-preview", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environment string `json:"environment"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); e != nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("预检参数无效"))
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		p, e := s.prepareRedisNode(ctx, "preview", []int64{id}, req.Environment)
		writeIntegrationJSON(w, p, e)
	})
}
