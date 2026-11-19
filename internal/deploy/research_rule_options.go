package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type researchRuleField struct {
	Values json.RawMessage `json:"values"`
}

type researchRuleForm struct {
	// ZenTao 16 uses fieldParams; some integrations expose params instead.
	FieldParams map[string]researchRuleField `json:"fieldParams"`
	Params      map[string]researchRuleField `json:"params"`
}

func researchRuleValues(field researchRuleField) map[string]string {
	var values map[string]sourceCode
	if json.Unmarshal(field.Values, &values) != nil || len(values) > 2000 {
		return nil
	}
	out := map[string]string{}
	for code, label := range values {
		// Native search sentinels are operators, not literal rule values.
		if code != "" && code != "all" && code != "null" && len(code) <= 191 && strings.TrimSpace(string(label)) != "" && len(label) <= 512 {
			out[code] = string(label)
		}
	}
	return out
}

func (z *zentaoLegacy) researchRuleOptions(ctx context.Context, c *ResourceConnection) (map[string]map[string]string, error) {
	options := map[string]map[string]string{"kind": {"bug": "Bug", "requirement": "需求", "task": "任务"}, "status": {"active": "激活", "resolved": "已解决", "closed": "已关闭"}, "project": {}, "product": {}, "resolution": sourceLabels(c, "resolution"), "severity": sourceLabels(c, "severity"), "priority": sourceLabels(c, "priority")}
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	defer z.resetLiveSearch()
	var browse liveSourcePage
	if err := z.call(ctx, "bug", "browse", url.Values{"productID": {"0"}, "browseType": {"all"}, "recPerPage": {"1"}, "pageID": {"1"}}, nil, &browse); err != nil {
		return nil, err
	}
	products := []int{}
	for id, label := range browse.Products {
		n, err := strconv.Atoi(id)
		if err == nil && liveProductAllowed(c, n) {
			options["product"][id] = label
			products = append(products, n)
		}
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("当前来源账号没有可读取的已配置产品")
	}
	sort.Ints(products)
	// Search metadata depends on the selected product. Read each visible,
	// configured product (connection validation caps this at 20), never only
	// the last product in the source session. Each browse fetches just one Bug.
	for _, product := range products {
		var page struct {
			ProductID sourceCode `json:"productID"`
		}
		id := strconv.Itoa(product)
		if err := z.call(ctx, "bug", "browse", url.Values{"productID": {id}, "browseType": {"all"}, "recPerPage": {"1"}, "pageID": {"1"}}, nil, &page); err != nil {
			return nil, err
		}
		if page.ProductID != "" && string(page.ProductID) != id {
			return nil, fmt.Errorf("来源返回的产品范围不匹配，未采用其他产品的选项")
		}
		var form researchRuleForm
		if err := z.call(ctx, "search", "buildForm", url.Values{"module": {"bug"}}, nil, &form); err != nil {
			return nil, err
		}
		for dest, src := range map[string]string{"project": "project", "resolution": "resolution", "severity": "severity", "priority": "pri", "status": "status"} {
			values := researchRuleValues(form.FieldParams[src])
			if len(values) == 0 {
				values = researchRuleValues(form.Params[src])
			}
			for code, label := range values {
				options[dest][code] = label
			}
		}
	}
	return options, nil
}
