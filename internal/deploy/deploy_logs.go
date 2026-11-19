package deploy

import (
	"database/sql"
	"sort"
	"strings"
)

// Defense in depth for restored historical logs and callers other than the
// streaming emitter. Never serialize the encrypted environment itself.
func (s *Store) redactDeployLog(id int64, text string) string {
	var encrypted string
	if err := s.db.QueryRow(`SELECT r.runtime_env_encrypted FROM release_orders r JOIN release_order_deploys rd ON rd.release_id=r.id WHERE rd.deploy_id=? LIMIT 1`, id).Scan(&encrypted); err != nil && err != sql.ErrNoRows {
		return "发布日志暂不可展示：无法读取脱敏快照"
	}
	values, err := s.decryptRuntimeEnvironment(encrypted)
	if err != nil {
		return "发布日志暂不可展示：运行变量脱敏信息无法读取"
	}
	secrets := []string{}
	for _, v := range values {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, v := range secrets {
		text = strings.ReplaceAll(text, v, "[REDACTED]")
	}
	return redactDeploymentText(text)
}
