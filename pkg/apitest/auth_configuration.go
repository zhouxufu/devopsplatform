package apitest

import (
	"encoding/json"
	"fmt"
	"net/url"
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
		credential := SecretKey(key) || strings.EqualFold(key, "pwd") || strings.EqualFold(key, "pass") || strings.Contains(strings.ToLower(key), "user") || strings.Contains(strings.ToLower(key), "account") || strings.Contains(strings.ToLower(key), "client_id")
		remainder := strings.TrimSpace(variable.ReplaceAllString(value, ""))
		reference := variable.MatchString(value) && (remainder == "" || remainder == "Bearer" || remainder == "Basic")
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
						if SecretKey(k) || strings.EqualFold(k, "pwd") {
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
	for _, f := range r.Form {
		if _, err := ReferenceAuth(Auth{Headers: map[string]string{f.Name: f.Value}}); err != nil {
			return err
		}
	}
	u, err := url.Parse(r.Path)
	if err != nil {
		return fmt.Errorf("请求路径无效")
	}
	for k, rows := range u.Query() {
		for _, v := range rows {
			if _, err := ReferenceAuth(Auth{Headers: map[string]string{k: v}}); err != nil {
				return err
			}
		}
	}
	return nil
}
