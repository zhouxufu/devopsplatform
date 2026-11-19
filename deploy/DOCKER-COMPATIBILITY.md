# Docker 发布兼容与升级

## 平台检查范围

- 部署目标：Docker CLI 可连接 Engine，服务端为 Linux，双方 API 至少 1.25，客户端 API 不低于服务端最低要求。此为协议基线，不是 Docker 发行版的全量认证清单。
- 停止旧容器前读取版本并检查 CLI 实际命令参数：run 的 detach/restart/name/env-file，以及配置用到的 mount/network/publish、额外允许参数；stop 的 time、image inspect 的 format；镜像文件发布另查 image load 的 input。缺失、无权限、守护进程不可达、响应无法解析均明确失败，不 pull/load/stop/rename/run。
- 保留归档 SHA-256、镜像 ID、架构校验及原容器回滚。移除 run --pull=never，不因较老客户端不认识该参数而失败。
- 构建 Runner：单独检查 Docker 连接、Buildx 插件和 builder 可用性，失败时不登录仓库、不构建、不推送。普通部署目标不要求 Buildx。
- 这些检查不能验证全部内核、存储驱动、镜像格式、架构模拟及业务兼容性；19.03/29.0 等响应仅用于合成测试，不应写成已实机认证的版本范围。版本升级应选发行版仍支持、维护中的 Docker 稳定补丁版。
- 协议依据：[Docker API 1.25 Mounts](https://docs.docker.com/reference/api/engine/version-history/#v125-api-changes)。平台更新时需要包含部署后台和 `steps/docker-build/run.sh`，不只更换前端。

## 隔离 Maven 构建读取宿主 settings.xml

- 明确配置的 settings.xml 由平台进程先读取，平台不可读、不是普通文件或超过 4 MiB 时，在拉镜像前报错，不改变文件权限。
- 每次构建使用独立临时目录（0700）和 settings 副本（0600），不放入源码、构建缓存或制品。副本只读挂载到原容器路径，所以旧构建快照中的绝对路径保持有效；本机非隔离构建维持原行为。
- 使用 `ro,Z` 给私有副本设置容器标签，不对原 settings 或整个 /home/config 目录 relabel，不添加特权模式，也不关闭 SELinux。Docker 对 `Z` 的定义及风险见[官方绑定挂载文档](https://docs.docker.com/engine/storage/bind-mounts/#configure-the-selinux-label)。
- 构建成功、执行失败及拉镜像等正常返回路径均清理宿主临时副本。进程被强杀、Docker 清理不确定等异常仍需核对；不承诺跨进程崩溃的凭据自动清理。配置中引用的额外证书等文件不会因此自动获得挂载权限。
- 本地已测试副本权限、原文件不变、私有只读挂载参数、读取失败前置、失败清理及不向命令行注入配置内容。2026-11-05 本机 Docker 引擎不可达，未完成真实容器和 CentOS SELinux enforcing 联调，不能把参数验收当作服务器实测。

## 192.168.0.3：先确认安装方式

2026-10-22 只读 SSH 尝试认证失败，未读到发行版及 Docker Client/Server 版本，未升级或重启该机。请在该机运行以下只读命令，输出中不包含容器环境变量或凭据：

```sh
cat /etc/os-release
uname -m
docker version
command -v docker
docker info --format 'root={{.DockerRootDir}} driver={{.Driver}}'
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
```

再按系统查看包来源（只运行存在的包管理器）：

```sh
dpkg-query -W docker-ce docker-ce-cli docker.io containerd.io
apt-cache policy docker-ce docker-ce-cli docker.io
```

或者：

```sh
rpm -q docker docker-ce docker-ce-cli containerd.io
dnf repolist
```

不要在不明安装来源时覆盖 Docker 静态二进制、切换软件源或卸载 docker/containerd/runc。若为 CentOS 7 等停止维护的系统，不能套用当前 CentOS Stream 安装命令；优先迁移到受支持系统再升级。

## 升级步骤（维护窗口）

1. 先记录 Docker/内核/存储驱动、现有包版本、容器、镜像和数据根目录；备份 daemon 配置、systemd 覆盖项、业务数据及部署文件。使用一致性磁盘/VM 快照或停机备份；运行中直接复制 Docker 数据目录不能作为可靠回退。
2. 确认可恢复的旧版本包和维护窗口。升级 Engine/containerd 可能重启服务并影响所有容器，不能只看 estate-ai-seek，也不能承诺 live-restore 完全无中断。不运行 prune，不删除 Docker 数据目录。
3. 以下命令只适用于**已使用 Docker 官方 CE 仓库、且系统仍被当前官方文档支持**的机器；若为 docker.io、旧 docker 包、静态包或第三方发行版，先按对应官方迁移步骤确认，不直接执行。

Ubuntu / Docker CE，先刷新并查询候选版本：

```sh
sudo apt-get update
apt-cache madison docker-ce
apt-cache madison docker-ce-cli
```

选择 Engine 和 CLI 都存在的同一完整版本串，将下方占位替换后先模拟，再实际安装：

```sh
sudo apt-get -s install docker-ce='<完整版本串>' docker-ce-cli='<完整版本串>' containerd.io
sudo apt-get install docker-ce='<完整版本串>' docker-ce-cli='<完整版本串>' containerd.io
```

受支持的 CentOS Stream / RHEL / Docker CE，先查询版本和依赖事务，再在维护窗口升级选定版本，不加自动同意参数：

```sh
sudo dnf list --showduplicates docker-ce docker-ce-cli
sudo dnf upgrade --assumeno docker-ce docker-ce-cli containerd.io
sudo dnf upgrade docker-ce docker-ce-cli containerd.io
```

只有该机兼任构建 Runner 时才另外安装 Docker Buildx 插件（官方 CE 包名 `docker-buildx-plugin`）；Compose 不属于本平台普通容器发布的必需依赖。

4. 安装后检查 `docker version`、`systemctl is-active docker`、容器列表、业务健康及日志；确认数据根目录和存储驱动未意外变化。需要重启 Docker 时在窗口内明确执行，不能在业务时段盲目重启。
5. 失败时按事先保存的系统/数据快照和旧包方案恢复；不能只降级二进制就承诺新版本数据目录兼容。验证完成后再由用户明确发起平台发布，不自动重发 AI 服务。

官方文档：[Ubuntu](https://docs.docker.com/engine/install/ubuntu/)、[CentOS](https://docs.docker.com/engine/install/centos/)、[RHEL](https://docs.docker.com/engine/install/rhel/)。适用发行版和软件包列表以对应页面为准。
