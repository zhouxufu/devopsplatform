package deploy

import "fmt"

// Called under the same lifecycle lock used by host updates/deletion. Groups
// remain optional; explicit hosts are persisted without creating hidden groups.
func resolveEnvironmentHosts(tx *deployTx, v *ServiceEnvironment) error {
	if v.ManagedTarget() || v.ManualTarget() || v.ScriptTarget() {
		return validateReleasePolicy(*v, len(v.Hosts))
	}
	if v.HostGroup != "" {
		var env string
		if err := tx.QueryRow(`SELECT environment FROM host_groups WHERE name=?`, v.HostGroup).Scan(&env); err != nil {
			return fmt.Errorf("主机组不存在: %s", v.HostGroup)
		}
		if env != v.Environment {
			return fmt.Errorf("主机组 %s 属于 %s 环境，不能绑定到 %s", v.HostGroup, env, v.Environment)
		}
		rows, err := tx.Query(`SELECT host FROM host_group_hosts WHERE group_name=? ORDER BY sort_order,host`, v.HostGroup)
		if err != nil {
			return err
		}
		v.Hosts = nil
		for rows.Next() {
			var host string
			if err = rows.Scan(&host); err != nil {
				rows.Close()
				return err
			}
			v.Hosts = append(v.Hosts, host)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	if len(v.Hosts) == 0 {
		return fmt.Errorf("请选择该环境下至少一台发布主机")
	}
	seen := map[string]bool{}
	for _, name := range v.Hosts {
		if seen[name] {
			return fmt.Errorf("主机重复: %s", name)
		}
		seen[name] = true
		var env string
		if err := tx.QueryRow(`SELECT environment FROM hosts WHERE name=?`, name).Scan(&env); err != nil {
			return fmt.Errorf("主机不存在: %s", name)
		}
		if env != v.Environment {
			return fmt.Errorf("主机 %s 属于 %s 环境，不能绑定到 %s", name, env, v.Environment)
		}
	}
	return validateReleasePolicy(*v, len(v.Hosts))
}
