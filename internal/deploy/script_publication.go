package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const TargetScript = "script"

func (v ServiceEnvironment) ScriptTarget() bool { return v.TargetType == TargetScript }

func scriptArguments(c map[string]string) ([]string, error) {
	args := []string{}
	if raw := strings.TrimSpace(c["script_arguments"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return nil, fmt.Errorf("脚本参数须为 JSON 字符串数组")
		}
	}
	if len(args) > 30 {
		return nil, fmt.Errorf("脚本参数最多 30 项")
	}
	for _, a := range args {
		if len(a) > 4096 || strings.ContainsRune(a, 0) {
			return nil, fmt.Errorf("脚本参数含非法内容或超过 4096 字节")
		}
	}
	return args, nil
}

func scriptRunnerDigest(r *BuildRunner) string {
	if r == nil {
		return ""
	}
	return digestJSON([]any{r.Name, r.Kind, r.Enabled, r.MaxParallel})
}

func (s *Store) validateScriptRunner(c map[string]string) (*BuildRunner, error) {
	r := s.GetRunner(c["script_runner"])
	if r == nil || !r.Enabled || r.Kind != "local" {
		return nil, fmt.Errorf("脚本发布请选择已启用的本机 Runner")
	}
	if c["script_runner_digest"] != scriptRunnerDigest(r) {
		return nil, fmt.Errorf("脚本 Runner 已变化，请重新保存环境配置并审核")
	}
	return r, nil
}

func (s *Store) validateScriptTarget(v *ServiceEnvironment) error {
	c := v.DeployConfig
	if strings.TrimSpace(c["script"]) == "" || len(c["script"]) > 64<<10 || strings.ContainsRune(c["script"], 0) {
		return fmt.Errorf("请填写发布脚本（Bash，最多 64 KiB）")
	}
	if _, err := scriptArguments(c); err != nil {
		return err
	}
	timeout, err := strconv.Atoi(c["script_timeout_seconds"])
	if err != nil || timeout < 1 || timeout > 3600 {
		return fmt.Errorf("脚本发布超时须为 1–3600 秒")
	}
	key := strings.TrimSpace(c["script_concurrency_key"])
	if len(key) > 191 || strings.ContainsAny(key, "\x00\r\n") {
		return fmt.Errorf("脚本并发标识无效")
	}
	name := c["script_runner"]
	if name == "" {
		name = s.GetServiceDelivery(v.Service).Runner
		if name == "" {
			name = "local"
		}
	}
	r := s.GetRunner(name)
	if r == nil || !r.Enabled || r.Kind != "local" {
		return fmt.Errorf("脚本发布请选择已启用的本机 Runner")
	}
	v.DeployConfig = map[string]string{"script": c["script"], "script_arguments": c["script_arguments"], "script_timeout_seconds": strconv.Itoa(timeout), "script_runner": name, "script_runner_digest": scriptRunnerDigest(r), "script_concurrency_key": key}
	v.Hosts, v.HostGroup, v.RemoteDir, v.StartCmd, v.StopCmd, v.HealthURL = nil, "", "", "", "", ""
	v.Strategy, v.BatchSize, v.MaxParallel, v.PauseBetweenBatches, v.FailureThreshold = "rolling", 1, 1, false, 0
	return nil
}

// ScriptPublicationResult is application-defined evidence, never executable input.
type ScriptPublicationResult struct {
	Status       string            `json:"status"`
	ExternalID   string            `json:"external_id,omitempty"`
	Values       map[string]string `json:"values,omitempty"`
	ReconciledBy string            `json:"reconciled_by,omitempty"`
	Comment      string            `json:"comment,omitempty"`
}

type scriptPublicationClaim struct {
	DeployID     int64  `json:"deploy_id"`
	Service      string `json:"service"`
	Environment  string `json:"environment"`
	ArtifactID   int64  `json:"artifact_id"`
	Digest       string `json:"digest"`
	ConfigDigest string `json:"config_digest"`
}

func scriptPublicationLock(service, environment string, c map[string]string) string {
	if key := c["script_concurrency_key"]; key != "" {
		return "script-publication-lock:" + digestJSON([]string{"resource", key})
	}
	return "script-publication-lock:" + digestJSON([]string{"scope", service, environment})
}

func scriptScopeKey(service, environment string) string {
	return "script-publication-scope:" + digestJSON([]string{service, environment})
}
func scriptResultKey(id int64) string { return fmt.Sprintf("script-publication-result:%d", id) }
func (s *Store) scriptWorkspace(id int64) string {
	return filepath.Join(s.dataDir, "script-publications", strconv.FormatInt(id, 10))
}

func (s *Store) scriptPublicationBusy(service, environment string, c map[string]string) error {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, scriptPublicationLock(service, environment, c)).Scan(&raw)
	if err == nil {
		return fmt.Errorf("%w: 此脚本目标存在执行中或结果待核对的发布；请先核对原发布结果", ErrRunConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

type scriptDriver struct{}

func (scriptDriver) Manifest() DeployDriverManifest {
	return DeployDriverManifest{APIVersion: "deploy.logplatform.io/v1", ID: TargetScript, Name: "脚本发布", Accepts: []string{ArtifactJar, ArtifactStatic, ArtifactJavaBundle, ArtifactDockerArchive}, Capabilities: []string{"deploy", "immutable-artifact", "script-result"}}
}
func (scriptDriver) Validate(sv *Service, _ Host, a Artifact) error {
	if a.Kind != ArtifactJar && a.Kind != ArtifactStatic && a.Kind != ArtifactJavaBundle && a.Kind != ArtifactDockerArchive {
		return fmt.Errorf("脚本发布需要可下载的文件制品")
	}
	if err := requireLocalArtifact(a, a.Kind); err != nil {
		return err
	}
	if _, err := scriptArguments(sv.DeployConfig); err != nil {
		return err
	}
	if strings.TrimSpace(sv.DeployConfig["script"]) == "" {
		return fmt.Errorf("发布脚本为空")
	}
	n, err := strconv.Atoi(sv.DeployConfig["script_timeout_seconds"])
	if err != nil || n < 1 || n > 3600 {
		return fmt.Errorf("脚本发布超时须为 1–3600 秒")
	}
	return nil
}
func (scriptDriver) Deploy(d *Deployer, id int64, sv *Service, _ Host, a Artifact, _ string) string {
	return d.deployScript(id, sv, a)
}

func readScriptResult(file string) (*ScriptPublicationResult, error) {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<10 {
		return nil, fmt.Errorf("脚本未写入有效结果文件（JSON，最多 32 KiB）")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var result ScriptPublicationResult
	if json.Unmarshal(raw, &result) != nil || (result.Status != "success" && result.Status != "pending" && result.Status != "failed") || len(result.ExternalID) > 512 || strings.ContainsAny(result.ExternalID, "\x00\r\n") || len(result.Values) > 32 {
		return nil, fmt.Errorf("脚本结果格式无效")
	}
	for k, v := range result.Values {
		if !mavenParamNameRE.MatchString(k) || len(k) > 64 || len(v) > 4096 || sensitiveParamRE.MatchString(k) {
			return nil, fmt.Errorf("脚本结果字段无效或含凭据字段")
		}
	}
	result.ReconciledBy, result.Comment = "", ""
	return &result, nil
}

func sanitizeScriptResult(result *ScriptPublicationResult, secrets map[string]string) *ScriptPublicationResult {
	copy := *result
	copy.Values = map[string]string{}
	clean := func(v string) string {
		for _, secret := range secrets {
			if secret != "" {
				v = strings.ReplaceAll(v, secret, "[REDACTED]")
			}
		}
		return redactDeploymentText(v)
	}
	copy.ExternalID = clean(copy.ExternalID)
	copy.Comment = clean(copy.Comment)
	for k, v := range result.Values {
		copy.Values[k] = clean(v)
	}
	return &copy
}
func (s *Store) saveScriptResult(id int64, result *ScriptPublicationResult, secrets map[string]string) error {
	raw, _ := json.Marshal(sanitizeScriptResult(result, secrets))
	_, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, scriptResultKey(id), string(raw))
	return err
}

func (s *Store) scriptResult(id int64) *ScriptPublicationResult {
	var raw string
	if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, scriptResultKey(id)).Scan(&raw) != nil {
		return nil
	}
	var result ScriptPublicationResult
	if json.Unmarshal([]byte(raw), &result) != nil {
		return nil
	}
	return &result
}

func copyScriptArtifact(source, destination, digest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual, _, err := artifactMetadata(destination)
	if err != nil || actual != digest {
		return fmt.Errorf("传入脚本的制品摘要校验失败")
	}
	return nil
}

func (d *Deployer) deployScript(id int64, sv *Service, a Artifact) string {
	c := sv.DeployConfig
	runner, err := d.st.validateScriptRunner(c)
	if err != nil {
		d.emit(id, err.Error()+"\n")
		return "failed"
	}
	if d.st.pipelineRunner == nil {
		d.emit(id, "Runner 执行器未初始化\n")
		return "failed"
	}
	release, ok := d.st.pipelineRunner.claimRunner(runner.Name, runner.MaxParallel)
	if !ok {
		d.emit(id, "Runner 已达到并发上限，未执行脚本\n")
		return "failed"
	}
	defer release()
	if err = d.st.scriptPublicationBusy(sv.Name, sv.RuntimeEnvironment, c); err != nil {
		d.emit(id, err.Error()+"\n")
		return "failed"
	}
	work, err := filepath.Abs(d.st.scriptWorkspace(id))
	if err != nil {
		return "failed"
	}
	if err = os.MkdirAll(work, 0700); err != nil {
		return "failed"
	}
	claimed := false
	defer func() {
		if !claimed {
			_ = os.RemoveAll(work)
		}
	}()
	file := filepath.Join(work, "artifact"+filepath.Ext(a.URI))
	resultFile := filepath.Join(work, "result.json")
	if err = copyScriptArtifact(a.URI, file, a.Digest); err != nil {
		d.emit(id, err.Error()+"\n")
		return "failed"
	}
	scriptFile := filepath.Join(work, "publish.sh")
	if err = os.WriteFile(scriptFile, []byte("printf '%s\\n' \"$$\" > \"$LP_PROCESS_FILE\"\n"+c["script"]), 0500); err != nil {
		return "failed"
	}
	args, _ := scriptArguments(c)
	timeout, _ := strconv.Atoi(c["script_timeout_seconds"])
	d.mu.Lock()
	parent := d.contexts[id]
	d.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{scriptFile}, args...)...)
	cmd.Dir = work
	// Deliberately exclude the server process environment and build credentials.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8"}
	for k, v := range sv.RuntimeEnv {
		if strings.HasPrefix(k, "LP_") || strings.HasPrefix(k, "DELIVERY_") {
			d.emit(id, "应用变量不能覆盖脚本发布上下文\n")
			return "failed"
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range map[string]string{"LP_WORKSPACE": work, "LP_ARTIFACT_PATH": file, "LP_ARTIFACT_ID": strconv.FormatInt(a.ID, 10), "LP_ARTIFACT_DIGEST": a.Digest, "LP_ARTIFACT_KIND": a.Kind, "LP_SERVICE": sv.Name, "LP_VERSION": a.Version, "LP_PROCESS_FILE": filepath.Join(work, "process.pid"), "LP_RESULT_FILE": resultFile, "DELIVERY_ENVIRONMENT": sv.RuntimeEnvironment, "DELIVERY_DEPLOY_ID": strconv.FormatInt(id, 10)} {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	claim := scriptPublicationClaim{id, sv.Name, sv.RuntimeEnvironment, a.ID, a.Digest, digestJSON(c)}
	raw, _ := json.Marshal(claim)
	key := scriptPublicationLock(sv.Name, sv.RuntimeEnvironment, c)
	tx, err := d.st.db.Begin()
	if err != nil {
		return "failed"
	}
	for _, k := range []string{key, scriptScopeKey(sv.Name, sv.RuntimeEnvironment)} {
		if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, k, string(raw)); err != nil {
			tx.Rollback()
			d.emit(id, "脚本目标已被其他发布占用，未执行\n")
			return "failed"
		}
	}
	if tx.Commit() != nil {
		return "failed"
	}
	claimed = true
	// Durable claim precedes the first script side effect. A restart never replays it.
	code := runBuildCommand(cmd, func(line string) { d.emit(id, line) })
	result, resultErr := readScriptResult(resultFile)
	if resultErr == nil {
		if err = d.st.saveScriptResult(id, result, sv.RuntimeEnv); err != nil {
			d.emit(id, "脚本结果保存失败，须核对\n")
			return "interrupted"
		}
		if result.ExternalID != "" {
			d.st.SetDeployExternalID(id, sanitizeScriptResult(result, sv.RuntimeEnv).ExternalID)
		}
	}
	if code != 0 || ctx.Err() != nil || resultErr != nil || result.Status != "success" {
		d.emit(id, "脚本执行结果须核对；未自动重试或回滚。执行目录与结果文件已保留。\n")
		return "interrupted"
	}
	return "success"
}

func (d *Deployer) finishScriptPublication(id int64, sv *Service, status string) {
	if sv.TargetType != TargetScript || status != "success" {
		return
	}
	var raw string
	key := scriptPublicationLock(sv.Name, sv.RuntimeEnvironment, sv.DeployConfig)
	if d.st.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&raw) != nil {
		return
	}
	var claim scriptPublicationClaim
	if json.Unmarshal([]byte(raw), &claim) != nil || claim.DeployID != id {
		return
	}
	tx, err := d.st.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, k := range []string{key, scriptScopeKey(sv.Name, sv.RuntimeEnvironment)} {
		r, e := tx.Exec(`DELETE FROM deploy_meta WHERE key=? AND value=?`, k, raw)
		if e != nil {
			return
		}
		n, e := r.RowsAffected()
		if e != nil || n != 1 {
			return
		}
	}
	if tx.Commit() == nil {
		_ = os.RemoveAll(d.st.scriptWorkspace(id))
	}
}
