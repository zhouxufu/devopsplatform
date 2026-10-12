package deploy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

func policyInt(cfg map[string]string, key string, fallback int) int {
	if v, err := strconv.Atoi(cfg[key]); err == nil {
		return v
	}
	return fallback
}
func canaryTarget(t ServiceEnvironment) bool { return t.DeployConfig["release_mode"] == "canary" }
func validateReleasePolicy(t ServiceEnvironment, count int) error {
	mode := t.DeployConfig["release_mode"]
	if mode == "" {
		return nil
	}
	if mode != "batch" && mode != "canary" {
		return fmt.Errorf("发布方式仅支持金丝雀或分批")
	}
	if mode := t.DeployConfig["batch_mode"]; mode != "" && mode != "manual" && mode != "auto" {
		return fmt.Errorf("批次推进仅支持 manual 或 auto")
	}
	for key, bounds := range map[string][2]int{"canary_count": {1, 10000}, "observe_seconds": {0, 3600}, "batch_count": {1, 10000}, "batch_wait_minutes": {0, 30}} {
		if raw := t.DeployConfig[key]; raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < bounds[0] || n > bounds[1] {
				return fmt.Errorf("发布参数 %s 超出范围", key)
			}
		}
	}
	if mode == "canary" && count > 0 && (count < 2 || policyInt(t.DeployConfig, "canary_count", 1)*2 > count) {
		return fmt.Errorf("金丝雀需要至少两个实例，首批不得超过实例总数的 50%%")
	}
	return nil
}
func saeUpdatePolicy(t ServiceEnvironment) (string, error) {
	if err := validateReleasePolicy(t, 0); err != nil {
		return "", err
	}
	mode := t.DeployConfig["release_mode"]
	if mode == "" {
		return t.DeployConfig["update_strategy"], nil
	}
	batch := map[string]any{"batch": policyInt(t.DeployConfig, "batch_count", 2), "releaseType": "auto", "batchWaitTime": policyInt(t.DeployConfig, "batch_wait_minutes", 0)}
	if mode == "canary" || t.DeployConfig["batch_mode"] != "auto" {
		batch["releaseType"] = "manual"
	}
	v := map[string]any{"type": "BatchUpdate", "batchUpdate": batch}
	if mode == "canary" {
		v["type"] = "GrayBatchUpdate"
		v["grayUpdate"] = map[string]int{"gray": policyInt(t.DeployConfig, "canary_count", 1)}
	}
	raw, err := json.Marshal(v)
	return string(raw), err
}
func releaseBatchEnd(o *ReleaseOrder) int {
	size := o.BatchSize
	if canaryTarget(o.Target) && o.NextIndex == 0 {
		size = policyInt(o.Target.DeployConfig, "canary_count", 1)
	}
	end := o.NextIndex + size
	if end > len(o.Hosts) {
		end = len(o.Hosts)
	}
	return end
}
func (s *Store) saeReleaseRecovery(o *ReleaseOrder) bool {
	if !o.Target.ManagedTarget() || len(o.DeployIDs) != 1 {
		return false
	}
	var flag string
	s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, fmt.Sprintf("sae-rollback:%d", o.DeployIDs[0])).Scan(&flag)
	return flag == "true"
}
func (s *Store) releaseContinueAfter(o *ReleaseOrder) int64 {
	if o.Recovery {
		return 0
	}
	if o.Status != "waiting_next_batch" || !canaryTarget(o.Target) {
		return 0
	}
	if o.Target.ManagedTarget() && len(o.DeployIDs) == 1 {
		var count string
		s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, fmt.Sprintf("sae-confirmations:%d", o.DeployIDs[0])).Scan(&count)
		if policyInt(map[string]string{"n": count}, "n", 0) > 0 {
			return 0
		}
	}
	if !o.Target.ManagedTarget() && o.NextIndex != policyInt(o.Target.DeployConfig, "canary_count", 1) {
		return 0
	}
	latest := int64(0)
	for _, id := range o.DeployIDs {
		if d := s.GetDeploy(id); d != nil && d.EndedAt > latest {
			latest = d.EndedAt
		}
	}
	if latest == 0 {
		latest = time.Now().Unix()
	}
	return latest + int64(policyInt(o.Target.DeployConfig, "observe_seconds", 300))
}
