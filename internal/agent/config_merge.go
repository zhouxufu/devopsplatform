package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"gopkg.in/yaml.v3"
)

// MergeCollectionConfig preserves the independently provisioned deployment
// stanza when the resource center updates log/metric settings. It never prints
// credentials and refuses to change an established deployment execution user.
func MergeCollectionConfig(current, proposed, runUser string) error {
	oldBytes, e := os.ReadFile(current)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	newBytes, e := os.ReadFile(proposed)
	if e != nil {
		return e
	}
	var old, next yaml.Node
	if yaml.Unmarshal(oldBytes, &old) != nil || yaml.Unmarshal(newBytes, &next) != nil || len(old.Content) != 1 || len(next.Content) != 1 {
		return fmt.Errorf("invalid Agent configuration")
	}
	oldMap, newMap := old.Content[0], next.Content[0]
	if oldMap.Kind != yaml.MappingNode || newMap.Kind != yaml.MappingNode {
		return fmt.Errorf("invalid Agent configuration mapping")
	}
	var deployment *yaml.Node
	for i := 0; i < len(oldMap.Content); i += 2 {
		if oldMap.Content[i].Value == "deployment" {
			deployment = oldMap.Content[i+1]
		}
	}
	if deployment == nil {
		return nil
	}
	for i := 0; i < len(newMap.Content); i += 2 {
		if newMap.Content[i].Value == "deployment" {
			return fmt.Errorf("log configuration updates cannot replace deployment policy")
		}
	}
	c, e := LoadConfig(current)
	if e != nil {
		return e
	}
	if c.Deployment.Enabled {
		var identity struct {
			UID int `json:"uid"`
		}
		b, e := os.ReadFile(filepath.Join(c.Deployment.StateDir, "owner.json"))
		if e == nil {
			if json.Unmarshal(b, &identity) != nil {
				return fmt.Errorf("invalid deployment execution identity")
			}
			u, e := user.Lookup(runUser)
			if e != nil {
				return e
			}
			uid, e := strconv.Atoi(u.Uid)
			if e != nil || uid != identity.UID {
				return fmt.Errorf("deployment execution user cannot change during a log configuration update")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	newMap.Content = append(newMap.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "deployment"}, deployment)
	merged, e := yaml.Marshal(&next)
	if e != nil {
		return e
	}
	var check Config
	if e = yaml.Unmarshal(merged, &check); e != nil {
		return e
	}
	if check.Deployment.Enabled && check.Deployment.Token == check.Token {
		return fmt.Errorf("deployment and ingest credentials must remain independent")
	}
	original, e := os.Stat(proposed)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(proposed), ".merged-agent-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if st, ok := original.Sys().(*syscall.Stat_t); ok {
		if e = f.Chown(int(st.Uid), int(st.Gid)); e != nil {
			f.Close()
			return e
		}
	}
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(merged)
	}
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, proposed); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(proposed))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
