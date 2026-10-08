# 应用构建与标准 Java 发布

目标是直接配置“用什么工具构建、发布到哪里、如何判断服务可用”，不需要复制 Jpom 的 Docker 命令或启停 DSL。

## 构建环境

入口：**接入/编辑应用 → 构建方式 → 构建环境**，也可在 **应用 → 构建** 或 **交付设置** 修改。

- **本机工具**：兼容旧应用，沿用 Runner 已安装的工具和 PATH/JAVA_HOME，不自动修改系统工具。
- **隔离容器**：按次启动临时构建容器，完成或超时后清理。可选 JDK 8/17/21 + Maven 3.9.9 或 Gradle 8.14.3，Node.js 20/22 + npm。
- **自定义镜像**：适合 Java + Node 组合、特殊编译工具、固定版本或内网镜像，例如已有的 `estate-gis-builder:java21-node22`。必须指定标签或 digest，镜像应包含 bash，并允许以 Runner 的 UID/GID 运行；不支持通过表单填写额外 Docker 参数或任意宿主机目录挂载。

`auto` 优先使用仓库里的 `mvnw` / `gradlew`，没有 Wrapper 时才使用所选环境内的 mvn / gradle。Wrapper 的版本不会被环境模板覆盖。平台对常见的 Gradle Wrapper/JDK 不兼容组合提前报错；完整兼容矩阵以 [Gradle 官方说明](https://docs.gradle.org/current/userguide/compatibility.html) 为准。

运行要求：

1. Docker 必须安装在**执行部署插件的 Runner 主机**上，平台运行账号能够访问 Docker 引擎。当前仍是本地 Runner，不是远程执行集群。
2. 首次构建可能下载镜像和 Wrapper/项目依赖；内网可先 `docker load` 导入镜像，使用自定义内网镜像并配置依赖仓库。不会自动修改宿主机 DNS 或使用 host 网络。
3. 镜像标签先解析为本地 image ID，再按该 ID 执行，实际 ID 和工具版本进入构建日志。已有本地镜像不自动更新；需要升级时由管理员更新镜像或更改配置。
4. 仅编译步骤进入容器；源码拉取和制品归档仍在宿主机。Docker 镜像构建继续使用本机 Docker，不挂载 Docker socket 到编译容器。
5. 挂载当前工作区、步骤脚本（只读）、输出目录和按应用/镜像隔离的缓存；Maven settings.xml 按配置只读挂载。HOME 为 `/lp-home`，Gradle 用户目录为 `/lp-home/.gradle`。不挂载整份宿主机 HOME、SSH 密钥或 Jpom secrets。
6. 构建变量组显式注入，敏感值不放 Docker 命令行；不要用变量组覆盖 HOME/PATH/JAVA_HOME/GRADLE_USER_HOME。Gradle 私服凭据仍通过 `GRADLE_REPOSITORY_URL/USERNAME/PASSWORD` 供项目读取。

此模式用于工具依赖隔离，**不是运行恶意代码的安全沙箱**。Docker 引擎访问权限接近主机管理员权限；不要开放给不可信用户，也没有自动资源配额或缓存淘汰功能。

## estate-ai 的推荐填写

| 项目 | 配置 |
| --- | --- |
| 应用 | `estate-ai-seek`，开发/测试共用一个应用 |
| 仓库分支 | `test-release` |
| 构建工具 | Gradle |
| 执行方式 | `auto` |
| 构建环境 | 隔离容器 → JDK 21 + Gradle；无需 Node 时不选 GIS 组合镜像 |
| 任务 | `clean :estate-ai-seek-bootstrap:bootJar` |
| 排除任务 | `test` |
| 产物 | `estate-ai-seek-bootstrap/build/libs/*.jar` |

平台的 JAR 打包步骤会排除 `*-plain.jar`、sources 和 javadoc，归档制品并计算摘要。不再需要运行 `deploy/jpom/build-release.sh`，也不需要打包 Jpom 的启动脚本。若项目另有构建前处理，应保留相应步骤，不能仅凭模板推定已完整迁移。

GIS 若确实同时需要 Java 21 与 Node 22，选择自定义组合镜像即可。构建步骤里的命令直接写 Gradle/npm 命令，不再套一层 `docker run`。

## 一键部署

完成代码、构建和环境配置后，在应用标题区点击 **一键部署**，选择目标环境即可把一次操作串成：拉取代码 → 构建 → 登记不可变制品 → 创建发布单 → 自动发布。构建失败不会触发发布；普通环境会直接执行，受保护环境只创建待审批发布单，不会绕过审批。流水线详情会显示本次目标环境，并在构建完成后补充自动创建的发布单编号。

“开始构建”仍只生成制品，适合验证代码或后续手工选择发布版本；“一键部署”适合日常把当前分支直接交付到一个已经配置好的环境。开始前平台会检查主机组、审批人、发布原因和是否已有进行中的发布单，避免构建成功后才发现目标不可用。

## 标准 Java 发布

入口：**应用 → 环境 → 配置部署环境 → 标准 Java**。新建 Java 环境默认标准模式；旧环境继续使用原自定义命令，不自动切换。

1. 选择环境和该环境的主机组，填写独立部署目录（例如 `/home/work/zfb/ai/devops-service`）。
2. 选择运行 JDK 21，填写主机上的 JDK 目录 `/home/work/runtime/jdk-21`；留空使用主机 PATH 中的 java。不会自动安装 JDK。
3. JVM 参数每行一个，不加 shell 引号，例如：

   ```text
   -Xms256m
   -Xmx1024m
   -XX:+UseG1GC
   -Dfile.encoding=UTF-8
   -Duser.timezone=Asia/Shanghai
   ```

4. 应用参数每行一个，例如 `--server.port=8889`；**就绪检查端口**填 `8889`。端口字段仅用于检查，不改变程序监听配置。也可以填写应用真实存在的完整 HTTP 健康检查 URL，例如 `http://127.0.0.1:8889/actuator/health`；不要只填路径或盲填不存在的 actuator 地址。
5. 环境变量按环境配置并加密保存。例如开发环境设置 `SPRING_PROFILES_ACTIVE=dev`，测试设置 `SPRING_PROFILES_ACTIVE=test`；Nacos 注册 IP、网卡名及服务依赖地址分别按目标环境设置。模型 API Key 等敏感值只放环境变量，不写 JVM 参数或脚本。
6. **健康检查失败时**默认停止新版本并恢复旧版本。非保护环境也可选择“停止新版本、不恢复旧版本”或“保留新版本运行”以便排障；这两种情况发布仍标记失败并停止后续批次。受保护环境固定自动回滚。

发布过程：校验制品及应用 class 的 JDK 要求 → 上传新版本 → 检查主机 JDK → 优雅停止旧版本 → 切换 current → 启动 → 进程 + 端口/HTTP 就绪检查。嵌套依赖包的 JDK 要求仍需构建测试与启动检查确认。

每个版本拥有独立的 `app.jar`、`service.sh` 和权限受限的 `runtime.env`；回滚恢复旧制品及旧变量。进程 PID 必须对应当前版本的精确 JAR 路径，不能只凭 PID 或宽泛 pkill 杀进程。停机超时不会自动强杀；没有旧版本时首次发布失败不会显示为“已回滚”。

发布完成后可进入 **应用 → 环境 → 实例管理**，实时读取每台主机的进程、PID 和实际版本，并执行启动、停止、重启与就绪检查。实例操作与发布共用目录锁，发布进行中不能并发启停；受保护环境需要二次确认和操作原因，成功或失败都会写入审计日志。

目录示例：

```text
devops-service/
  current -> releases/deploy-123
  releases/deploy-123/{app.jar,service.sh,runtime.env}
  logs/application.log
  .devopsplatform.pid
  .devopsplatform-owner
```

注意：

- 标准模式只支持 Linux，使用 bash、coreutils；HTTP 检查需要 curl，检查地址从部署主机访问。端口检查使用 127.0.0.1，应用需监听该地址或通配地址。
- 不会自动停止/接管 Jpom 的进程。首次迁移先确认新配置，在发布窗口停止旧服务或选择独立端口，避免双平台争用。旧 Jpom 配置暂时保留作为人工回退方案。
- 单机替换存在短暂停机；这不是零停机部署。主机组可沿用平台分批发布机制。
- 当前标准模式使用受控的后台 Java 进程，不提供开机自启、崩溃自动重启；需要进程守护的场景继续使用自定义 systemd 命令。
- 历史版本和应用日志暂不自动清理，需要运维设置保留策略。`runtime.env` 是主机上权限受限的明文快照，应限制 SSH 用户和备份访问。
- 发布锁记录操作所有者和开始时间。升级前遗留的空锁会在下一次操作时自动恢复；带所有者的活动锁不会被其他发布删除，超过 30 分钟才会作为陈旧锁回收。日志会明确打印锁所有者和年龄，仍可据此排查异常中断。
