package deploy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

func redisReadKeys(a []string) ([]string, error) {
	bad := fmt.Errorf("只读命令或参数不支持；SCAN/HSCAN/SSCAN/ZSCAN 使用游标与 COUNT 1–100，LRANGE/ZRANGE 每次最多 100 项；写命令请提交变更")
	number := func(s string, max uint64) bool { n, e := strconv.ParseUint(s, 10, 64); return e == nil && n <= max }
	count := func(s string) bool { n, e := strconv.ParseUint(s, 10, 64); return e == nil && n >= 1 && n <= 100 }
	if len(a) == 0 {
		return nil, bad
	}
	switch a[0] {
	case "GET":
		if len(a) == 2 {
			return a[1:], nil
		}
	case "PING", "TYPE", "TTL", "PTTL", "STRLEN", "HLEN", "LLEN", "SCARD", "ZCARD", "EXISTS":
		return redisCommandKeys(a)
	case "SCAN":
		if len(a) == 6 && number(a[1], ^uint64(0)) && strings.EqualFold(a[2], "MATCH") && strings.EqualFold(a[4], "COUNT") && count(a[5]) {
			return nil, nil
		}
	case "HSCAN", "SSCAN", "ZSCAN":
		if len(a) == 5 && number(a[2], ^uint64(0)) && strings.EqualFold(a[3], "COUNT") && count(a[4]) {
			return a[1:2], nil
		}
	case "HGET":
		if len(a) == 3 {
			return a[1:2], nil
		}
	case "LRANGE", "ZRANGE":
		if len(a) == 4 && number(a[2], 100000) && number(a[3], 100000) {
			start, _ := strconv.Atoi(a[2])
			end, _ := strconv.Atoi(a[3])
			if end >= start && end-start < 100 {
				return a[1:2], nil
			}
		}
	}
	return nil, bad
}
func validateRedisReadScope(commands [][]string, c *ResourceConnection) error {
	prefix := c.Config["key_prefix"]
	if prefix == "" || strings.ContainsAny(prefix, "*?[]") {
		return fmt.Errorf("资源必须配置准确 Key 前缀")
	}
	for _, a := range commands {
		if _, e := redisReadKeys(a); e != nil {
			return e
		}
		if a[0] == "SCAN" {
			if strings.Contains(prefix, `\`) {
				return fmt.Errorf("此资源前缀含转义字符，不支持 SCAN；请使用准确 Key 查询")
			}
			pattern := a[3]
			if !strings.HasPrefix(pattern, prefix) || strings.ContainsAny(pattern[:len(prefix)], "*?[]") || len(pattern) > 512 {
				return fmt.Errorf("SCAN MATCH 必须以资源的精确 Key 前缀开始")
			}
			continue
		}
		keys, e := redisReadKeys(a)
		if e != nil {
			return e
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) || strings.ContainsAny(key, "*?[]") {
				return fmt.Errorf("查询 Key 必须匹配资源前缀，精确 Key 不允许通配符")
			}
		}
	}
	return nil
}

// Bound RESP2 headers before go-redis allocates a bulk/array response. COUNT
// is only a hint: compact hashes and malicious replies may exceed it.
type boundedRESPConn struct {
	net.Conn
	reader  *bufio.Reader
	pending []byte
}

type redisResponseLimitError struct{ message string }

func (e *redisResponseLimitError) Error() string { return e.message }

func (c *boundedRESPConn) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		if c.reader == nil {
			c.reader = bufio.NewReaderSize(c.Conn, 256)
		}
		var b bytes.Buffer
		if e := readBoundedRESP(c.reader, &b, 0); e != nil {
			return 0, e
		}
		c.pending = b.Bytes()
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func readBoundedRESP(r *bufio.Reader, b *bytes.Buffer, depth int) error {
	if depth > 8 {
		return &redisResponseLimitError{"Redis 响应嵌套过深"}
	}
	line, e := r.ReadSlice('\n')
	if e != nil {
		return fmt.Errorf("Redis 响应头异常或连接中断")
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return fmt.Errorf("Redis 响应无效")
	}
	if b.Len()+len(line) > 2<<20 {
		return &redisResponseLimitError{"Redis 响应超过 2 MiB，请缩小范围"}
	}
	b.Write(line)
	switch line[0] {
	case '+', '-', ':':
		return nil
	case '$', '*':
		n, e := strconv.Atoi(string(line[1 : len(line)-2]))
		if e != nil || n < -1 {
			return fmt.Errorf("Redis 响应长度无效")
		}
		if n == -1 {
			return nil
		}
		if line[0] == '*' {
			if n > 1000 {
				return &redisResponseLimitError{"Redis 集合返回超过 1000 项，请缩小范围"}
			}
			for i := 0; i < n; i++ {
				if e := readBoundedRESP(r, b, depth+1); e != nil {
					return e
				}
			}
			return nil
		}
		if n > 65537 || b.Len()+n+2 > 2<<20 {
			return &redisResponseLimitError{"Redis 单个值超过 64 KiB，请通过业务接口查看"}
		}
		v := make([]byte, n+2)
		if _, e = io.ReadFull(r, v); e != nil {
			return e
		}
		if v[n] != '\r' || v[n+1] != '\n' {
			return fmt.Errorf("Redis 响应结尾无效")
		}
		b.Write(v)
		return nil
	default:
		return fmt.Errorf("仅支持 RESP2 查询响应")
	}
}
