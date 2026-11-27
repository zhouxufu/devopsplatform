package apitest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func loginBody(a Auth, vars map[string]string) (string, error) {
	if a.BodyType != "form" {
		return expandBody(a.Body, vars)
	}
	values, err := url.ParseQuery(a.Body)
	if err != nil {
		return "", fmt.Errorf("登录表单格式无效")
	}
	for key, rows := range values {
		for i, value := range rows {
			rows[i], err = expand(value, vars)
			if err != nil {
				return "", err
			}
		}
		values[key] = rows
	}
	return values.Encode(), nil
}

// ReferenceAuth is the MCP boundary: secrets stay in the existing encrypted
// application variables. Legacy inline credentials are never exported to MCP.
func ReferenceAuth(a Auth) (Auth, error) { return referenceAuth(a, true) }

var requestCredential = regexp.MustCompile(`^(?:(?i:Bearer|Basic) +)?\{\{(?:var|step)\.[A-Za-z_][A-Za-z0-9_]*\}\}$`)

func credentialField(key string) bool {
	key = strings.ToLower(key)
	switch key {
	case "user", "username", "user_name", "account", "client_id", "pwd", "pass", "auth", "jwt", "credential", "signature", "access_key", "accesskey":
		return true
	}
	return SecretKey(key) || strings.Contains(key, "session") || strings.HasSuffix(key, "-auth") || strings.HasSuffix(key, "_auth")
}

func validRequestField(key, value string) bool {
	return value == "" || !credentialField(key) || IsCredentialReference(value)
}

func IsCredentialReference(value string) bool {
	return requestCredential.MatchString(strings.TrimSpace(value))
}

func referenceAuth(a Auth, strictBody bool) (Auth, error) {
	clone, _ := json.Marshal(a)
	a.Headers = nil
	_ = json.Unmarshal(clone, &a)
	var invalid bool
	// Login credentials belong in the JSON/form template, not a URL. Do not
	// export legacy query strings (including non-standard credential names).
	if strictBody {
		if u, err := url.Parse(a.Path); err == nil && u.RawQuery != "" {
			u.RawQuery = ""
			a.Path = u.String()
			invalid = true
		}
	}
	check := func(key, value string) string {
		k := strings.ToLower(key)
		credential := credentialField(k)
		if strictBody && k != "user-agent" {
			credential = credential || strings.Contains(k, "user") || strings.Contains(k, "account") || strings.Contains(k, "client_id")
		}
		remainder := strings.TrimSpace(variable.ReplaceAllString(value, ""))
		reference := variable.MatchString(value) && (remainder == "" || remainder == "Bearer" || remainder == "Basic")
		if !strictBody {
			reference = IsCredentialReference(value)
		}
		if credential && value != "" && !reference {
			invalid = true
			return "[REDACTED]"
		}
		return value
	}
	checkBody := func(key, value string) string {
		if strictBody {
			switch strings.ToLower(key) {
			case "grant_type", "scope", "audience":
			default:
				if value != "" && (!variable.MatchString(value) || strings.TrimSpace(variable.ReplaceAllString(value, "")) != "") {
					invalid = true
					return "[REDACTED]"
				}
			}
		}
		return check(key, value)
	}

	for key, value := range a.Headers {
		a.Headers[key] = check(key, value)
		if strictBody && value != "" {
			switch strings.ToLower(key) {
			case "content-type", "accept", "user-agent":
			default:
				remainder := strings.TrimSpace(variable.ReplaceAllString(value, ""))
				if !variable.MatchString(value) || (remainder != "" && remainder != "Bearer" && remainder != "Basic") {
					invalid = true
					a.Headers[key] = "[REDACTED]"
				}
			}
		}
	}
	if a.BodyType == "form" {
		values, err := url.ParseQuery(a.Body)
		if err != nil {
			a.Body = "[REDACTED]"
			return a, fmt.Errorf("登录表单格式无效")
		}
		for k, rows := range values {
			for i, v := range rows {
				rows[i] = checkBody(k, v)
			}
			values[k] = rows
		}
		a.Body = values.Encode()
	} else if a.Body != "" {
		var value any
		if json.Unmarshal([]byte(a.Body), &value) != nil {
			a.Body = "[REDACTED]"
			return a, fmt.Errorf("登录 JSON 无效")
		}
		var walk func(any) any
		walk = func(v any) any {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if text, ok := v.(string); ok {
						x[k] = checkBody(k, text)
					} else {
						if credentialField(k) {
							invalid = true
							x[k] = "[REDACTED]"
						} else {
							x[k] = walk(v)
						}
					}
				}
			case []any:
				for i, v := range x {
					if text, ok := v.(string); ok {
						x[i] = checkBody("", text)
					} else {
						x[i] = walk(v)
					}
				}
			}
			return v
		}
		raw, _ := json.Marshal(walk(value))
		a.Body = string(raw)
	}
	if invalid {
		if !strictBody {
			return a, fmt.Errorf("请求凭据须引用 {{var.NAME}} 或上游提取的 {{step.NAME}}")
		}
		return a, fmt.Errorf("登录凭据必须引用受保护变量，例如 {{var.API_PASSWORD}}；请先在应用环境绑定变量")
	}
	return a, nil
}

func ValidateReferences(r Request) error {
	check := Auth{Headers: r.Headers}
	if r.Body != "" {
		if !json.Valid([]byte(r.Body)) {
			return fmt.Errorf("MCP 请求正文请使用 JSON 或结构化表单")
		}
		check.Body = r.Body
	}
	if _, err := referenceAuth(check, false); err != nil {
		return err
	}
	u, err := url.Parse(r.Path)
	if err != nil {
		return fmt.Errorf("请求路径无效")
	}
	fields, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("请求查询参数无效")
	}
	for _, part := range strings.Split(u.RawQuery, "&") {
		key, encoded, _ := strings.Cut(part, "=")
		name, _ := url.QueryUnescape(key)
		value, _ := url.QueryUnescape(encoded)
		if credentialField(name) && IsCredentialReference(value) && !requestVariable.MatchString(encoded) {
			return fmt.Errorf("凭据参数 %s 的变量引用请保留原始双花括号", name)
		}
	}
	for _, f := range r.Form {
		fields.Add(f.Name, f.Value)
	}
	for k, rows := range fields {
		for _, v := range rows {
			if !validRequestField(k, v) {
				return fmt.Errorf("参数 %s 的凭据须引用 {{var.NAME}} 或上游提取的 {{step.NAME}}", k)
			}
		}
	}
	return nil
}
