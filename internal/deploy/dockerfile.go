package deploy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

func validateDockerfileParams(params map[string]string) error {
	switch params["DOCKERFILE_SOURCE"] {
	case "template":
		if params["DOCKERFILE_TEMPLATE_VERSION"] != "" && params["DOCKERFILE_TEMPLATE_VERSION"] != "1" {
			return fmt.Errorf("不支持的 Dockerfile 模板版本")
		}
		if _, err := dockerfileTemplate(params); err != nil {
			return err
		}
	case "", "repository":
		if strings.TrimSpace(params["DOCKERFILE"]) == "" || params["DOCKERFILE"] == "-" || strings.ContainsAny(params["DOCKERFILE"], "\x00\r\n") {
			return fmt.Errorf("请填写仓库 Dockerfile 路径；标准输入仅用于平台托管模式")
		}
	case "platform":
		content := params["DOCKERFILE_CONTENT"]
		if strings.TrimSpace(content) == "" || len(content) > 32<<10 || strings.ContainsRune(content, '\x00') {
			return fmt.Errorf("平台托管 Dockerfile 必须非空、不含 NUL，且不超过 32 KiB")
		}
	default:
		return fmt.Errorf("Dockerfile 来源仅支持基础模板、仓库文件或自定义内容")
	}
	return nil
}

// Version 1 must remain available for historical build retries.
func dockerfileTemplate(params map[string]string) (string, error) {
	template := params["DOCKERFILE_TEMPLATE"]
	if template == "" {
		template = "java21"
	}
	artifactPath := strings.TrimSpace(params["DOCKERFILE_ARTIFACT_PATH"])
	if artifactPath == "" {
		if template == "nginx" {
			artifactPath = "dist"
		} else {
			artifactPath = "app.jar"
		}
	}
	if path.IsAbs(artifactPath) || strings.ContainsAny(artifactPath, "\x00\r\n\\") || artifactPath == ".." || strings.HasPrefix(artifactPath, "../") || strings.Contains(artifactPath, "/../") {
		return "", fmt.Errorf("模板产物路径必须位于构建上下文内")
	}
	switch template {
	case "java21":
		copyLine, _ := json.Marshal([]string{artifactPath, "/app/app.jar"})
		return "FROM eclipse-temurin:21-jre-jammy\nWORKDIR /app\nCOPY " + string(copyLine) + "\nEXPOSE 8888\nENTRYPOINT [\"java\",\"-jar\",\"/app/app.jar\"]\n", nil
	case "nginx":
		copyLine, _ := json.Marshal([]string{artifactPath, "/usr/share/nginx/html/"})
		return "FROM nginx:stable\nCOPY " + string(copyLine) + "\nEXPOSE 80\n", nil
	default:
		return "", fmt.Errorf("基础模板仅支持 Java 21 JAR 或 Nginx 静态站点")
	}
}

func resolvedDockerfileParams(params map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range params {
		out[key] = value
	}
	if params["DOCKERFILE_SOURCE"] == "template" {
		content, _ := dockerfileTemplate(params)
		out["DOCKERFILE_CONTENT"], out["DOCKERFILE_TEMPLATE_VERSION"] = content, "1"
	}
	return out
}

func dockerfileBuildMetadata(params map[string]string) map[string]string {
	if params["DOCKERFILE_SOURCE"] == "platform" || params["DOCKERFILE_SOURCE"] == "template" {
		resolved := resolvedDockerfileParams(params)
		return map[string]string{"dockerfile_source": params["DOCKERFILE_SOURCE"], "dockerfile_digest": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(resolved["DOCKERFILE_CONTENT"]))), "dockerfile_template": params["DOCKERFILE_TEMPLATE"], "dockerfile_template_version": resolved["DOCKERFILE_TEMPLATE_VERSION"]}
	}
	return map[string]string{"dockerfile_source": "repository", "dockerfile_path": params["DOCKERFILE"]}
}
