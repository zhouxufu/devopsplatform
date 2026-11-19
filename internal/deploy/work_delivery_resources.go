package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
)

// Resolve declarations inside the save transaction. Shared/group and catalog
// references use the same aliases shown in the editor; no target is contacted.
func (s *Store) workDeliveryResourceKinds(tx *deployTx, service string) (map[string]string, error) {
	read := func(key string, out any) (bool, error) {
		var raw string
		err := tx.QueryRow(s.resourceGroupLockQuery(), key).Scan(&raw)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if json.Unmarshal([]byte(raw), out) != nil {
			return false, fmt.Errorf("应用资源配置损坏，请先核对资源")
		}
		return true, nil
	}
	// Serialize against application resource CAS saves before locking groups.
	var revision string
	if err := tx.QueryRow(s.resourceGroupLockQuery(), "application_resources_revision:"+service).Scan(&revision); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var document ApplicationResources
	composed, err := read("application_resources_v2:"+service, &document)
	if err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	add := func(requirements []ApplicationResourceRequirement) error {
		for _, r := range requirements {
			if _, exists := kinds[r.Alias]; exists {
				return fmt.Errorf("资源别名 %s 冲突，请先核对资源", r.Alias)
			}
			kinds[r.Alias] = r.Kind
		}
		return nil
	}
	groups := append([]string{}, document.ResourceGroups...)
	if composed {
		if err = add(document.OwnRequirements); err != nil {
			return nil, err
		}
	} else {
		var group string
		err = tx.QueryRow(s.resourceGroupLockQuery(), "application_resource_group:"+service).Scan(&group)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if group != "" {
			groups = append(groups, group)
		} else {
			rows, e := tx.Query(`SELECT alias,kind FROM application_resource_requirements WHERE service=?`, service)
			if e != nil {
				return nil, e
			}
			for rows.Next() {
				var alias, kind string
				if e = rows.Scan(&alias, &kind); e != nil {
					rows.Close()
					return nil, e
				}
				kinds[alias] = kind
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
		}
	}
	sort.Strings(groups)
	for _, name := range groups {
		var group ResourceGroup
		found, e := read("resource_group:"+name, &group)
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, fmt.Errorf("资源组 %s 不存在", name)
		}
		if err = add(group.Requirements); err != nil {
			return nil, err
		}
	}
	refs := map[string]bool{}
	for _, ref := range document.NacosConfigs {
		if refs[ref.Alias] {
			continue
		}
		refs[ref.Alias] = true
		if err = add([]ApplicationResourceRequirement{{Alias: ref.Alias, Kind: "nacos"}}); err != nil {
			return nil, err
		}
	}
	return kinds, nil
}
