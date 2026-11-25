package devtools

import (
	"encoding/json"
	"fmt"
)

// Apply the declared change schema recursively. In particular, clients cannot
// smuggle internal IDs, execution plans or generated data-sync provenance into
// nested SQL / Nacos payloads which the UI never offered.
func validateChangeShape(schema map[string]any, value any, path string) error {
	if values, ok := schema["enum"].([]string); ok {
		v, _ := value.(string)
		valid := false
		for _, item := range values {
			if v == item {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("%s 的选项无效", path)
		}
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s 必须是对象", path)
		}
		props, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]string)
		for _, key := range required {
			if _, ok := object[key]; !ok {
				return fmt.Errorf("%s 缺少 %s", path, key)
			}
		}
		for key, v := range object {
			next, ok := props[key].(map[string]any)
			if !ok {
				return fmt.Errorf("%s 不允许字段 %s", path, key)
			}
			if err := validateChangeShape(next, v, path+"."+key); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		minItems, maxItems := 1, 200
		if minimum, yes := schema["minItems"].(int); yes {
			minItems = minimum
		}
		if maximum, yes := schema["maxItems"].(int); yes {
			maxItems = maximum
		}
		if !ok || len(items) < minItems || len(items) > maxItems {
			return fmt.Errorf("%s 需要 %d–%d 项", path, minItems, maxItems)
		}
		next, _ := schema["items"].(map[string]any)
		for i, v := range items {
			if err := validateChangeShape(next, v, fmt.Sprintf("%s[%d]", path, i+1)); err != nil {
				return err
			}
		}
	case "string":
		v, ok := value.(string)
		if !ok || len(v) > 1<<20 {
			return fmt.Errorf("%s 必须是字符串且不能超过 1 MiB", path)
		}
	case "integer":
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("%s 必须是正整数", path)
		}
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return fmt.Errorf("%s 必须是正整数", path)
		}
		v, err := n.Int64()
		if err != nil || v < 1 {
			return fmt.Errorf("%s 必须是正整数", path)
		}
	}
	return nil
}
