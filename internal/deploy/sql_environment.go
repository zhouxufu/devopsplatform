package deploy

import (
	"database/sql"
	"fmt"
	"strings"
)

// Overrides are rare exceptions to the common script, keyed by stable environment name.
type SQLEnvironmentOverride struct {
	Environment string `json:"environment"`
	Content     string `json:"content,omitempty"`
	Skip        bool   `json:"skip,omitempty"`
	Reason      string `json:"reason"`
}

// Freeze the selection and its default alongside the actual executable statements.
type SQLScriptSelection struct {
	ChangeID       int64  `json:"change_id"`
	Environment    string `json:"environment"`
	Content        string `json:"content,omitempty"`
	DefaultContent string `json:"default_content"`
	Skip           bool   `json:"skip,omitempty"`
	Reason         string `json:"reason"`
	ChangeDigest   string `json:"change_digest"`
}

func normalizeSQLEnvironmentOverrides(v *SQLChange) error {
	return normalizeSQLEnvironmentOverridesForSave(v, true)
}

func validateSQLDraftContent(content string) error {
	if len(content) > 64<<10 || strings.TrimSpace(content) == "" {
		return fmt.Errorf("SQL 必填且不超过 64 KiB")
	}
	if strings.ContainsRune(content, '\x00') {
		return fmt.Errorf("SQL 不能包含空字符")
	}
	return nil
}

func normalizeSQLEnvironmentOverridesForSave(v *SQLChange, strictSQL bool) error {
	if len(v.EnvironmentOverrides) > 20 {
		return fmt.Errorf("SQL 环境例外最多 20 项")
	}
	if v.DataSync != nil && len(v.EnvironmentOverrides) > 0 {
		return fmt.Errorf("数据同步 SQL 已冻结，不支持环境例外，请按目标环境重新对比")
	}
	seen := map[string]bool{}
	total := 0
	for i := range v.EnvironmentOverrides {
		x := &v.EnvironmentOverrides[i]
		x.Environment = strings.TrimSpace(x.Environment)
		x.Content = strings.TrimSpace(x.Content)
		x.Reason = strings.TrimSpace(x.Reason)
		if !validResourceName(x.Environment) || seen[x.Environment] {
			return fmt.Errorf("第 %d 个 SQL 环境例外：请选择有效且不重复的环境", i+1)
		}
		seen[x.Environment] = true
		if x.Reason == "" || len(x.Reason) > 2000 {
			return fmt.Errorf("第 %d 个 SQL 环境例外：请填写原因（不超过 2000 字节）", i+1)
		}
		if x.Skip {
			if x.Content != "" {
				return fmt.Errorf("第 %d 个 SQL 环境例外：无需执行时不能同时填写 SQL", i+1)
			}
		} else {
			if err := validateSQLDraftContent(x.Content); err != nil {
				return fmt.Errorf("第 %d 个 SQL 环境例外：%w", i+1, err)
			}
			if strictSQL {
				if _, err := parseChangeSQL(x.Content); err != nil {
					return fmt.Errorf("第 %d 个 SQL 环境例外：%w", i+1, err)
				}
			}
		}
		total += len(x.Content)
	}
	if total > 256<<10 {
		return fmt.Errorf("SQL 环境专用脚本总计不能超过 256 KiB")
	}
	return nil
}
func validateSQLOverrideEnvironments(q interface{ QueryRow(string, ...any) *sql.Row }, v *SQLChange) error {
	if v == nil {
		return nil
	}
	for _, x := range v.EnvironmentOverrides {
		var name string
		if err := q.QueryRow(`SELECT name FROM environments WHERE name=?`, x.Environment).Scan(&name); err != nil {
			return fmt.Errorf("SQL 环境例外的环境 %s 不存在或无法读取", x.Environment)
		}
	}
	return nil
}
func selectEnvironmentSQL(v *SQLChange, environment string) (SQLEnvironmentOverride, bool, error) {
	if v == nil {
		return SQLEnvironmentOverride{}, false, fmt.Errorf("缺少 SQL 配置")
	}
	// Validate all exceptions before selecting; malformed stored data never falls back silently.
	copy := *v
	copy.EnvironmentOverrides = append([]SQLEnvironmentOverride{}, v.EnvironmentOverrides...)
	if err := normalizeSQLEnvironmentOverrides(&copy); err != nil {
		return SQLEnvironmentOverride{}, false, err
	}
	for _, x := range copy.EnvironmentOverrides {
		if x.Environment == environment {
			return x, true, nil
		}
	}
	return SQLEnvironmentOverride{Environment: environment, Content: v.Content}, false, nil
}
func (s *Store) verifySQLScriptSelections(p *SQLExecutionPlan, environment string) error {
	for _, selection := range p.ScriptSelections {
		c := s.GetChange(selection.ChangeID)
		if c == nil || c.SQL == nil || c.Status != ChangeReady || digestJSON([]any{c.Service, c.SQL}) != selection.ChangeDigest || selection.Environment != environment {
			return fmt.Errorf("SQL 环境例外内容或状态已变化，请重新预检审批")
		}
		selected, explicit, err := selectEnvironmentSQL(c.SQL, environment)
		if err != nil || !explicit || selected.Skip != selection.Skip || selected.Content != selection.Content || selected.Reason != selection.Reason || c.SQL.Content != selection.DefaultContent {
			return fmt.Errorf("SQL 环境脚本选择已变化，请重新预检审批")
		}
	}
	return nil
}
