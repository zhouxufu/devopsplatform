package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type XXLJobChange struct {
	ResourceAlias     string                     `json:"resource_alias"`
	Action            string                     `json:"action"`
	TargetTitle       string                     `json:"target_title,omitempty"`
	TargetHandler     string                     `json:"target_handler,omitempty"`
	CreatedByChangeID int64                      `json:"created_by_change_id,omitempty"`
	Config            map[string]json.RawMessage `json:"config,omitempty"`
}

func normalizeXXLJobChange(change *Change) error {
	job := change.XXLJob
	if change.Kind != xxlJobKind || change.Service == "" || job == nil || !validResourceName(job.ResourceAlias) ||
		change.SQL != nil || change.Nacos != nil || change.Redis != nil {
		return fmt.Errorf("定时任务变更必须填写应用、执行器资源别名和结构化任务内容")
	}
	job.TargetTitle = strings.TrimSpace(job.TargetTitle)
	job.TargetHandler = strings.TrimSpace(job.TargetHandler)
	if job.CreatedByChangeID != 0 {
		if job.Action != "enable" || job.TargetTitle != "" || job.TargetHandler != "" ||
			!slices.ContainsFunc(change.Prerequisites, func(dep ChangePrerequisite) bool {
				return dep.ChangeID == job.CreatedByChangeID
			}) {
			return fmt.Errorf("启用本次新建的任务时，请将创建变更明确列为前置变更")
		}
	} else if job.Action == "create" {
		if job.TargetTitle != "" || job.TargetHandler != "" {
			return fmt.Errorf("新建任务请直接填写任务标题和 Handler")
		}
	} else if job.TargetTitle == "" || job.TargetHandler == "" || len(job.TargetTitle) > 255 || len(job.TargetHandler) > 255 {
		return fmt.Errorf("变更已有任务时，请填写准确的任务标题和 Handler")
	}
	switch job.Action {
	case "create", "update":
		if len(job.Config) == 0 {
			return fmt.Errorf("请填写任务配置")
		}
	case "enable", "disable":
		if len(job.Config) != 0 {
			return fmt.Errorf("启停变更只调整调度状态，请单独登记任务配置修改")
		}
	default:
		return fmt.Errorf("定时任务支持创建、修改、启用和停用")
	}
	stringsByKey := map[string]int{
		"jobDesc": 255, "author": 64, "alarmEmail": 255, "scheduleType": 16, "scheduleConf": 128,
		"misfireStrategy": 32, "executorRouteStrategy": 32, "executorHandler": 255,
		"executorParam": 512, "executorBlockStrategy": 32,
	}
	values := make(map[string]string, len(job.Config))
	for key, raw := range job.Config {
		if maxLength, ok := stringsByKey[key]; ok {
			var text string
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &text) != nil || len(text) > maxLength {
				return fmt.Errorf("任务配置 %s 必须是长度不超过 %d 字节的字符串", key, maxLength)
			}
			values[key] = strings.TrimSpace(text)
		} else if key == "executorTimeout" || key == "executorFailRetryCount" {
			var number int32
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &number) != nil || number < 0 {
				return fmt.Errorf("任务配置 %s 必须为非负整数", key)
			}
		} else {
			return fmt.Errorf("不支持任务配置字段 %s", key)
		}
	}
	if job.Action == "create" {
		if values["jobDesc"] == "" || values["executorHandler"] == "" || values["author"] == "" || values["scheduleType"] == "" {
			return fmt.Errorf("新建任务需要标题、Handler、负责人和调度类型")
		}
	}
	if schedule, provided := values["scheduleType"]; provided {
		switch schedule {
		case "CRON", "FIX_RATE", "NONE":
		default:
			return fmt.Errorf("调度类型须为 CRON、FIX_RATE 或 NONE")
		}
		if rule, provided := values["scheduleConf"]; schedule == "FIX_RATE" && (provided || job.Action == "create") {
			interval, err := strconv.Atoi(rule)
			if err != nil || interval < 1 {
				return fmt.Errorf("固定频率间隔须为正整数秒")
			}
		}
		if schedule == "CRON" && (job.Action == "create" || job.Config["scheduleConf"] != nil) && len(strings.Fields(values["scheduleConf"])) < 6 {
			return fmt.Errorf("请填写 6 或 7 段的 XXL-JOB Cron 表达式")
		}
	}
	return nil
}

func (s *Store) checkXXLChangeDeclaration(tx *deployTx, change *Change) error {
	if change.XXLJob == nil {
		return nil
	}
	kinds, err := s.workDeliveryResourceKinds(tx, change.Service)
	if err != nil {
		return err
	}
	if kinds[change.XXLJob.ResourceAlias] != xxlJobKind {
		return fmt.Errorf("应用 %s 未声明定时任务执行器资源 %s", change.Service, change.XXLJob.ResourceAlias)
	}
	return nil
}

func xxlChangeTouchKey(id int64) string {
	return fmt.Sprintf("xxl-job-change:%d", id)
}
