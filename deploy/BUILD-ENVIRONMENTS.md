# 构建工具镜像

在「应用 → 编辑配置 → 工具链与 Runner」选择隔离容器。Maven、Gradle、Node 预设沿用原行为；组合工具选择自定义镜像。

自定义镜像的「镜像缺失时」支持：

- 从镜像仓库拉取（默认，兼容现有配置）。
- 按仓库 Dockerfile 构建：先检出本次源码，再使用指定 Dockerfile 准备工具镜像，成功后才运行编译步骤。失败不会改为拉取同名镜像，也不会继续编译或归档。

本地已有该版本标签时直接复用，编译使用解析后的不可变镜像 ID。更新 Dockerfile 或其依赖文件时应更换工具镜像版本标签；缺失时的 Docker 构建可复用 Docker 层缓存。同一平台进程并发准备同名镜像会等待并重新核对，取消等待不会发起另一份构建。

Dockerfile 和上下文均相对于本次检出的源码根目录。上下文留空等同 `.`；拒绝绝对路径、父目录、`.git` 路径及通过符号链接越出源码目录的输入。遵循上下文中的 `.dockerignore`；镜像构建不注入业务构建变量或任意 build args，不挂载宿主机凭据和 Docker socket。构建镜像中的工具仍需支持以 Runner 的 UID/GID 执行。

例如 GIS 工具镜像的配置候选（不是已保存的业务配置）：

```json
{
  "mode": "container",
  "preset": "custom",
  "image": "estate-gis-builder:java21-node22",
  "network": "host",
  "image_source": "dockerfile",
  "dockerfile": "deploy/jpom/Dockerfile.build",
  "context": "."
}
```

`network` 同时用于镜像 Dockerfile 内的构建步骤及后续源码编译容器。Docker 引擎拉取 `FROM` 基础镜像仍需要仓库连通性或已导入的镜像；宿主机网络不能修复 Docker 引擎自身的仓库 DNS/访问故障。若 Dockerfile 需要 apt/npm 等依赖，对应源也须可访问。

工具镜像 Dockerfile 与业务运行镜像的 Dockerfile 分别配置，前者用于 Java/Node 等编译工具，后者用于最终发布的运行镜像。保存此配置不启动构建或发布；来源、路径和上下文随现有构建配置快照冻结，历史重试不会使用后来修改的路径。
