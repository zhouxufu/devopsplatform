package server

import "strings"

// levelFilter is shared by stored queries and live subscriptions. An empty
// selection is unrestricted; selected levels are combined with OR.
type levelFilter []string

func parseLevelFilter(raw string) levelFilter {
	var levels levelFilter
	seen := map[string]bool{}
	for _, value := range strings.Split(raw, ",") {
		level := normLevel(strings.TrimSpace(value))
		if level != "" && !seen[level] {
			levels = append(levels, level)
			seen[level] = true
		}
	}
	return levels
}

func (levels levelFilter) SQL() (string, []any) {
	if len(levels) == 0 {
		return "", nil
	}
	args := make([]any, len(levels))
	for i, level := range levels {
		args[i] = level
	}
	return "level COLLATE NOCASE IN (" + strings.TrimSuffix(strings.Repeat("?,", len(levels)), ",") + ")", args
}

func (levels levelFilter) Match(value string) bool {
	if len(levels) == 0 {
		return true
	}
	value = normLevel(strings.TrimSpace(value))
	for _, level := range levels {
		if level == value {
			return true
		}
	}
	return false
}
