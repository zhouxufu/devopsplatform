package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	sae "github.com/alibabacloud-go/sae-20190506/v4/client"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type saeDeployInput struct {
	AppID, NamespaceID, Version, Description string
	Artifact                                 Artifact
	Config                                   map[string]string
	RuntimeEnv                               map[string]string
}

type saeChangeState struct {
	Status     int32
	Message    string
	PipelineID string
	SubStatus  int32
}

type saeEnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func mergeSAEEnvironment(raw string, runtime map[string]string) (string, error) {
	items := []saeEnvironmentVariable{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return "", fmt.Errorf("SAE 环境变量 JSON 无效: %w", err)
		}
	}
	positions := map[string]int{}
	for i, item := range items {
		positions[item.Name] = i
	}
	keys := make([]string, 0, len(runtime))
	for key := range runtime {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if i, ok := positions[key]; ok {
			items[i].Value = runtime[key]
		} else {
			positions[key] = len(items)
			items = append(items, saeEnvironmentVariable{Name: key, Value: runtime[key]})
		}
	}
	if len(items) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type saeProvider interface {
	Test(context.Context) error
	Deploy(context.Context, saeDeployInput) (string, error)
	Change(context.Context, string) (saeChangeState, error)
}

type saeProviderFactory func(*ResourceConnection) (saeProvider, error)

type aliyunSAEProvider struct {
	client     *sae.Client
	connection *ResourceConnection
}

func newAliyunSAEProvider(connection *ResourceConnection) (saeProvider, error) {
	if connection == nil || connection.Kind != TargetAliyunSAE {
		return nil, fmt.Errorf("SAE 连接不存在或类型不正确")
	}
	region := strings.TrimSpace(connection.Config["region"])
	if region == "" || connection.Username == "" || connection.Password == "" {
		return nil, fmt.Errorf("SAE 连接缺少地域或 AccessKey")
	}
	endpoint := strings.TrimSpace(connection.Endpoint)
	if endpoint == "" {
		endpoint = "sae." + region + ".aliyuncs.com"
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	cfg := &openapi.Config{}
	cfg.SetAccessKeyId(connection.Username).SetAccessKeySecret(connection.Password).SetRegionId(region).SetEndpoint(endpoint)
	if token := strings.TrimSpace(connection.Config["security_token"]); token != "" {
		cfg.SetSecurityToken(token)
	}
	client, err := sae.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &aliyunSAEProvider{client: client, connection: connection}, nil
}

func (p *aliyunSAEProvider) Test(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resp, err := p.client.DescribeNamespaces((&sae.DescribeNamespacesRequest{}).SetCurrentPage(1).SetPageSize(1))
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil || resp.Body.Success == nil || !*resp.Body.Success {
		return fmt.Errorf("SAE 凭据校验失败: %s", stringValue(respMessage(resp)))
	}
	return nil
}

func respMessage(resp *sae.DescribeNamespacesResponse) *string {
	if resp == nil || resp.Body == nil {
		return nil
	}
	return resp.Body.Message
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (p *aliyunSAEProvider) Deploy(ctx context.Context, in saeDeployInput) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	req := (&sae.DeployApplicationRequest{}).SetAppId(in.AppID).SetChangeOrderDesc(in.Description)
	version := saePackageVersion(in.Version, in.Artifact.Digest)
	if in.Artifact.Kind == ArtifactJar {
		packageURL, err := p.uploadJar(in)
		if err != nil {
			return "", err
		}
		req.SetPackageType("FatJar").SetPackageUrl(packageURL).SetPackageVersion(version)
		jdk := strings.TrimSpace(in.Config["jdk"])
		if jdk == "" {
			jdk = "Open JDK 8"
		}
		req.SetJdk(jdk)
		if v := strings.TrimSpace(in.Config["jar_start_options"]); v != "" {
			req.SetJarStartOptions(v)
		}
		if v := strings.TrimSpace(in.Config["jar_start_args"]); v != "" {
			req.SetJarStartArgs(v)
		}
	} else if in.Artifact.Kind == ArtifactOCI {
		req.SetPackageType("Image").SetImageUrl(in.Artifact.URI)
		if v := strings.TrimSpace(in.Config["acr_instance_id"]); v != "" {
			req.SetAcrInstanceId(v)
		}
		if v := strings.TrimSpace(in.Config["command"]); v != "" {
			req.SetCommand(v)
		}
		if v := strings.TrimSpace(in.Config["command_args"]); v != "" {
			req.SetCommandArgs(v)
		}
	} else {
		return "", fmt.Errorf("SAE 仅支持 JAR 或 OCI 镜像制品")
	}
	if envs, err := mergeSAEEnvironment(in.Config["envs"], in.RuntimeEnv); err != nil {
		return "", err
	} else if envs != "" {
		req.SetEnvs(envs)
	}
	policy, err := saeUpdatePolicy(ServiceEnvironment{DeployConfig: in.Config})
	if err != nil {
		return "", err
	}
	if in.Config["release_mode"] != "" {
		resp, e := p.client.DescribeApplicationConfig((&sae.DescribeApplicationConfigRequest{}).SetAppId(in.AppID))
		if e != nil {
			return "", e
		}
		if resp == nil || resp.Body == nil || resp.Body.Success == nil || !*resp.Body.Success || resp.Body.Data == nil || resp.Body.Data.Replicas == nil {
			return "", fmt.Errorf("无法读取 SAE 实例数")
		}
		if in.Config["release_mode"] == "canary" && *resp.Body.Data.Replicas < 2 {
			return "", fmt.Errorf("SAE 金丝雀需要至少两个实例")
		}
		if err := validateReleasePolicy(ServiceEnvironment{TargetType: TargetAliyunSAE, DeployConfig: in.Config}, int(*resp.Body.Data.Replicas)); err != nil {
			return "", err
		}
	}
	if v := strings.TrimSpace(policy); v != "" {
		req.SetUpdateStrategy(v)
	}
	if v, err := optionalInt32(in.Config["min_ready_ratio"]); err != nil {
		return "", fmt.Errorf("最小可用比例: %w", err)
	} else if v != nil {
		req.SetMinReadyInstanceRatio(*v)
	}
	if v, err := optionalInt32(in.Config["batch_wait_seconds"]); err != nil {
		return "", fmt.Errorf("批次间隔: %w", err)
	} else if v != nil {
		if in.Config["release_mode"] == "" {
			req.SetBatchWaitTime(*v)
		}
	}
	resp, err := p.client.DeployApplication(req)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Body == nil || resp.Body.Success == nil || !*resp.Body.Success || resp.Body.Data == nil {
		message := "SAE DeployApplication 返回失败"
		if resp != nil && resp.Body != nil && resp.Body.Message != nil {
			message = *resp.Body.Message
		}
		return "", fmt.Errorf("%s", message)
	}
	changeID := stringValue(resp.Body.Data.ChangeOrderId)
	if changeID == "" {
		return "", fmt.Errorf("SAE 未返回变更单 ID")
	}
	return changeID, nil
}

func (p *aliyunSAEProvider) uploadJar(in saeDeployInput) (string, error) {
	bucketName := strings.TrimSpace(in.Config["oss_bucket"])
	if bucketName == "" {
		bucketName = strings.TrimSpace(p.connection.Config["oss_bucket"])
	}
	if bucketName == "" {
		return "", fmt.Errorf("JAR 发布缺少 OSS Bucket")
	}
	region := strings.TrimSpace(in.Config["region"])
	if region == "" {
		region = strings.TrimSpace(p.connection.Config["region"])
	}
	endpoint := strings.TrimSpace(in.Config["oss_endpoint"])
	if endpoint == "" {
		endpoint = strings.TrimSpace(p.connection.Config["oss_endpoint"])
	}
	if endpoint == "" {
		endpoint = "https://oss-" + region + ".aliyuncs.com"
	}
	options := []oss.ClientOption{}
	if token := strings.TrimSpace(p.connection.Config["security_token"]); token != "" {
		options = append(options, oss.SecurityToken(token))
	}
	client, err := oss.New(endpoint, p.connection.Username, p.connection.Password, options...)
	if err != nil {
		return "", err
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return "", err
	}
	prefix := strings.Trim(strings.TrimSpace(in.Config["oss_prefix"]), "/")
	if prefix == "" {
		prefix = strings.Trim(strings.TrimSpace(p.connection.Config["oss_prefix"]), "/")
	}
	if prefix == "" {
		prefix = "logplatform/releases"
	}
	digest := strings.TrimPrefix(in.Artifact.Digest, "sha256:")
	objectKey := prefix + "/" + safeSAEName(in.AppID) + "/" + digest + "/" + filepath.Base(in.Artifact.URI)
	if err := bucket.PutObjectFromFile(objectKey, in.Artifact.URI); err != nil {
		return "", fmt.Errorf("上传 JAR 到 OSS: %w", err)
	}
	url, err := bucket.SignURL(objectKey, oss.HTTPGet, 24*60*60)
	if err != nil {
		return "", fmt.Errorf("生成 OSS 下载地址: %w", err)
	}
	return url, nil
}

func (p *aliyunSAEProvider) Change(ctx context.Context, changeID string) (saeChangeState, error) {
	if err := ctx.Err(); err != nil {
		return saeChangeState{}, err
	}
	resp, err := p.client.DescribeChangeOrder((&sae.DescribeChangeOrderRequest{}).SetChangeOrderId(changeID))
	if err != nil {
		return saeChangeState{}, err
	}
	if resp == nil || resp.Body == nil || resp.Body.Success == nil || !*resp.Body.Success || resp.Body.Data == nil || resp.Body.Data.Status == nil {
		return saeChangeState{}, fmt.Errorf("读取 SAE 变更单失败")
	}
	state := saeChangeState{Status: *resp.Body.Data.Status, Message: stringValue(resp.Body.Data.ErrorMessage), PipelineID: stringValue(resp.Body.Data.CurrentPipelineId)}
	if resp.Body.Data.SubStatus != nil {
		state.SubStatus = *resp.Body.Data.SubStatus
	}
	return state, nil
}

func optionalInt32(raw string) (*int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("必须是整数")
	}
	n := int32(v)
	return &n, nil
}

var unsafeSAEName = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func safeSAEName(v string) string {
	v = strings.Trim(unsafeSAEName.ReplaceAllString(v, "-"), "-.")
	if v == "" {
		return "application"
	}
	return v
}

func saePackageVersion(version, digest string) string {
	version = safeSAEName(version)
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		digest = digest[:12]
	}
	value := strings.Trim(version+"-"+digest, "-")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

type aliyunSAEDriver struct{}

func (aliyunSAEDriver) Manifest() DeployDriverManifest {
	return DeployDriverManifest{
		APIVersion: "deploy.logplatform.io/v1", ID: TargetAliyunSAE, Name: "Alibaba Cloud SAE",
		Accepts:      []string{ArtifactJar, ArtifactOCI},
		Capabilities: []string{"deploy", "verify", "rollback", "managed-target", "immutable-digest", "jar-via-oss"},
		Params: []StepParam{
			{Name: "connection", Type: "input", Label: "SAE 连接"},
			{Name: "app_id", Type: "input", Label: "SAE 应用 ID"},
			{Name: "namespace_id", Type: "input", Label: "命名空间 ID"},
			{Name: "region", Type: "input", Label: "地域"},
			{Name: "oss_bucket", Type: "input", Label: "JAR 中转 OSS Bucket"},
			{Name: "jdk", Type: "input", Label: "JDK 运行时"},
		},
	}
}

func (aliyunSAEDriver) Validate(sv *Service, _ Host, artifact Artifact) error {
	cfg := sv.DeployConfig
	if strings.TrimSpace(cfg["connection"]) == "" || strings.TrimSpace(cfg["app_id"]) == "" || strings.TrimSpace(cfg["namespace_id"]) == "" || strings.TrimSpace(cfg["region"]) == "" {
		return fmt.Errorf("SAE 目标缺少连接、应用 ID、命名空间或地域")
	}
	if artifact.Kind == ArtifactJar {
		return requireLocalArtifact(artifact, ArtifactJar)
	}
	if artifact.Kind != ArtifactOCI || !strings.Contains(artifact.URI, "@sha256:") || !strings.HasSuffix(artifact.URI, artifact.Digest) {
		return fmt.Errorf("SAE 镜像发布必须使用 image@sha256:digest")
	}
	return nil
}

func (aliyunSAEDriver) Deploy(runtime *Deployer, deployID int64, sv *Service, _ Host, artifact Artifact, version string) string {
	connection := runtime.st.GetConnection(sv.DeployConfig["connection"])
	provider, err := runtime.saeFactory(connection)
	if err != nil {
		runtime.emit(deployID, "✗ 创建 SAE 客户端失败: "+err.Error()+"\n")
		return "failed"
	}
	timeout := 30 * time.Minute
	if minutes, err := strconv.Atoi(strings.TrimSpace(sv.DeployConfig["timeout_minutes"])); err == nil && minutes > 0 && minutes <= 180 {
		timeout = time.Duration(minutes) * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	runtime.emit(deployID, fmt.Sprintf("☁ SAE 应用 %s / 命名空间 %s / 地域 %s\n", sv.DeployConfig["app_id"], sv.DeployConfig["namespace_id"], sv.DeployConfig["region"]))
	if artifact.Kind == ArtifactJar {
		runtime.emit(deployID, "⇪ 上传不可变 JAR 到 OSS 并生成限时下载地址\n")
	} else {
		runtime.emit(deployID, "⬢ 使用不可变镜像 "+artifact.URI+"\n")
	}
	changeID, err := provider.Deploy(ctx, saeDeployInput{AppID: sv.DeployConfig["app_id"], NamespaceID: sv.DeployConfig["namespace_id"], Version: version, Description: "logplatform release " + version, Artifact: artifact, Config: sv.DeployConfig, RuntimeEnv: sv.RuntimeEnv})
	if err != nil {
		runtime.emit(deployID, "✗ SAE 发起发布失败: "+err.Error()+"\n")
		return "failed"
	}
	runtime.st.SetDeployExternalID(deployID, changeID)
	runtime.emit(deployID, "✓ SAE 变更单 "+changeID+" 已创建，开始轮询\n")
	return pollSAEChange(runtime, deployID, provider, ctx, changeID)
}

func pollSAEChange(runtime *Deployer, deployID int64, provider saeProvider, ctx context.Context, changeID string) string {
	return pollSAEChangeAfter(runtime, deployID, provider, ctx, changeID, "")
}
func pollSAEChangeAfter(runtime *Deployer, deployID int64, provider saeProvider, ctx context.Context, changeID, ignorePipeline string) string {
	poll := runtime.saePollInterval
	if poll <= 0 {
		poll = 5 * time.Second
	}
	lastStatus := int32(-1)
	for {
		state, err := provider.Change(ctx, changeID)
		if err != nil {
			runtime.emit(deployID, "✗ 查询 SAE 变更单失败: "+err.Error()+"\n")
			return "failed"
		}
		if state.SubStatus == 1 {
			runtime.emit(deployID, "✗ SAE 发布异常，停止平台推进；请核对云端变更并回退\n")
			return "failed"
		}
		if state.Status != 8 || state.PipelineID != ignorePipeline {
			ignorePipeline = ""
		}
		switch state.Status {
		case 2:
			runtime.emit(deployID, "✓ SAE 变更单执行成功\n")
			return "success"
		case 6:
			return "canceled"
		case 3, 10:
			message := strings.TrimSpace(state.Message)
			if message == "" {
				message = fmt.Sprintf("状态码 %d", state.Status)
			}
			runtime.emit(deployID, "✗ SAE 发布失败: "+message+"\n")
			return "failed"
		case 8:
			if ignorePipeline != "" {
				break
			}
			runtime.emit(deployID, "⏸ SAE 首批/当前批次完成，等待平台观察确认\n")
			return "waiting_next_batch"
		case 11:
			if state.Status != lastStatus {
				runtime.emit(deployID, "⏸ SAE 变更单等待云端确认或审批\n")
			}
		}
		lastStatus = state.Status
		select {
		case <-ctx.Done():
			runtime.emit(deployID, "✗ SAE 发布等待超时: "+ctx.Err().Error()+"\n")
			return "failed"
		case <-time.After(poll):
		}
	}
}
