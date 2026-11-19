package apitest

import (
	"fmt"
	"regexp"
	"strings"
)

// Profile is the existing application/environment test contract. Additional
// named entries do not replace the legacy default or alter historical runs.
type Entry struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	PathPrefix string `json:"path_prefix,omitempty"`
	Auth       Auth   `json:"auth"`
}
type Profile struct {
	Entries             []Entry `json:"entries,omitempty"`
	RegressionSuiteID   int64   `json:"regression_suite_id"`
	RegressionTargetKey string  `json:"regression_target_key"`
	Revision            int64   `json:"revision"`
	Enabled             bool    `json:"enabled"`
	BaseURL             string  `json:"base_url"`
	Auth                Auth    `json:"auth"`
}

func ValidateEntries(entries []Entry) error {
	if len(entries) > 10 {
		return fmt.Errorf("最多配置 10 个测试入口")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(e.Key) || seen[e.Key] || strings.TrimSpace(e.Label) == "" || len(e.Label) > 100 || (e.Kind != "direct" && e.Kind != "gateway") {
			return fmt.Errorf("入口标识、名称或类型无效或重复")
		}
		seen[e.Key] = true
		u, err := Base(e.BaseURL)
		if err != nil {
			return err
		}
		if u.Path != "" && u.Path != "/" {
			return fmt.Errorf("入口 Base URL 仅填写协议和主机，路径填写到路径前缀")
		}
		if e.PathPrefix != "" {
			if _, err = Join(e.BaseURL, e.PathPrefix); err != nil {
				return err
			}
			if strings.ContainsAny(e.PathPrefix, "?#") {
				return fmt.Errorf("路径前缀不能有查询或片段")
			}
		}
		if err = ValidateAuth(e.Auth); err != nil {
			return err
		}
	}
	return nil
}

func ValidateProfile(p Profile) error {
	if p.Revision < 0 || p.RegressionSuiteID < 0 {
		return fmt.Errorf("配置修订或回归测试集无效")
	}
	if p.BaseURL != "" {
		if _, err := Base(p.BaseURL); err != nil {
			return err
		}
	}
	if err := ValidateAuth(p.Auth); err != nil {
		return err
	}
	return ValidateEntries(p.Entries)
}
