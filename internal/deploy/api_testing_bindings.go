package deploy

import (
	"fmt"
	"logplatform/pkg/apitest"
	"strings"
)

// Explicit input mappings are revisioned with a suite. Extracted values stay in memory.
type apiTestCaseBinding struct {
	TargetCaseID int64  `json:"target_case_id"`
	Name         string `json:"name"`
	SourceCaseID int64  `json:"source_case_id"`
	SourceName   string `json:"source_name"`
	SourceRow    string `json:"source_row,omitempty"`
}

func (b apiTestCaseBinding) key() string { return fmt.Sprintf("%d:%s", b.TargetCaseID, b.Name) }
func validateAPITestBindings(bindings []apiTestCaseBinding, cases []*apiTestCase) error {
	if len(bindings) > 40 {
		return fmt.Errorf("测试集最多配置 40 个用例变量关联")
	}
	indices := map[int64]int{}
	for i, c := range cases {
		indices[c.ID] = i
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		source, ok := indices[b.SourceCaseID]
		target, exists := indices[b.TargetCaseID]
		if !ok || !exists || source >= target {
			return fmt.Errorf("变量来源必须是测试集中排在前面的用例，请检查执行顺序")
		}
		if !apiTestColumnRE.MatchString(b.Name) || !apiTestColumnRE.MatchString(b.SourceName) || seen[b.key()] {
			return fmt.Errorf("用例变量名称无效或重复")
		}
		seen[b.key()] = true
		c := cases[source]
		found := false
		for _, s := range c.Steps {
			for _, e := range s.Extractions {
				if e.Name == b.SourceName {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("来源用例 TC-%d 未声明提取变量 %s", c.ID, b.SourceName)
		}
		if c.Graph != nil {
			plan, err := planAPITestGraph(c)
			if err != nil {
				return err
			}
			if !plan.guaranteed[b.SourceName] {
				return fmt.Errorf("来源变量 %s 并非所有分支均会输出", b.SourceName)
			}
		}
		rowFound := b.SourceRow == "" && len(c.DataRows) <= 1
		for _, row := range c.DataRows {
			if row.Name == b.SourceRow {
				rowFound = true
			}
		}
		if !rowFound {
			return fmt.Errorf("来源用例有多组数据时，请明确选择来源数据组")
		}
	}
	for _, c := range cases {
		available := map[string]bool{}
		for _, b := range bindings {
			if b.TargetCaseID == c.ID {
				available["case."+b.Name] = true
			}
		}
		for _, step := range c.Steps {
			if step.Condition != nil && strings.HasPrefix(step.Condition.Variable, "case.") && !available[step.Condition.Variable] {
				return fmt.Errorf("条件用例变量未配置")
			}
			if e := apitest.ValidateCaseReferences(step.Request, available, true); e != nil {
				return fmt.Errorf("TC-%d：%w", c.ID, e)
			}
		}
	}
	return nil
}
