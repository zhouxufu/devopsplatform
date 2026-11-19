# VictoriaLogs 日志存储

日志正文、历史查询、分钟计数和历史回放统一使用 VictoriaLogs；没有 SQLite 日志后端开关、双写或旧库查询路径。搜索框继续使用平台原有语法，由服务端转换为 LogsQL。Agent 仍使用原 `/ingest`、gzip、event_id、batch_id 与完整 ACK 协议。

## 部署依赖

当前验收版本为官方 VictoriaLogs **v1.53.0**。`python3 scripts/fetch-victorialogs.py` 从官方 GitHub release 下载对应系统/架构的文件并验证官方 SHA-256，原子写入 bin；不会启动进程。Linux 发布包包含该官方二进制、`support/VICTORIALOGS-VERSION`、许可证、服务单元参考和本文；这些文件、平台版本与包内管理命令均受 `SHA256SUMS` 覆盖。打包增加 Python 3 与官方 release 网络访问要求，目标机安装时不再下载依赖。

本地运行：先执行下载命令，再运行原 `scripts/start-all.sh`。没有指定外部存储 URL 时，脚本在 loopback 9428 启动本地 VictoriaLogs；已有健康实例则复用。`stop-all.sh` 只停止平台/Agent 组件，独立日志存储继续运行。已有正式实例不要用相同数据目录同时启动第二个进程。

Linux/systemd 由统一管理命令安装和运维，首次安装见 [部署说明](README.md)。下例使用默认安装目录：

```sh
sudo /tmp/devopsplatformctl install /tmp/devopsplatform-<版本>-linux-amd64.tar.gz
sudo devopsplatformctl upgrade /tmp/devopsplatform-<新版本>-linux-amd64.tar.gz
sudo devopsplatformctl status
sudo devopsplatformctl logs victorialogs
sudo devopsplatformctl restart victorialogs
```

`start` 先启动存储并检查 `/health`，再启动平台；平台 systemd 的 `ExecStartPre` 同样等待存储，覆盖开机启动。`stop` 先停平台再停本机存储；普通 `restart` 保留健康且配置未变的存储进程。`restart victorialogs` 会先停平台、重启存储，依赖健康后恢复平台。健康检查不跟随重定向；外部 Bearer Token 经 curl stdin 配置传入，不进入进程参数或命令输出。

### 本机配置和升级

全新安装创建 `/home/devopsplatform/config/victorialogs.env`：

```ini
VLOGS_LISTEN_ADDR=127.0.0.1:9428
VLOGS_RETENTION_DAYS=7
VLOGS_DISK_LIMIT=20GiB
VLOGS_MEMORY_LIMIT=256MiB
```

已有文件不覆盖；现有平台保留天数或受支持旧单元的 `-retentionPeriod=365d` 会沿用。修改后执行 `sudo devopsplatformctl restart`，会应用变化的存储配置并同步日志插件保留天数。如果 `devopsplatform.env` 已显式设置 `DEVOPSPLATFORM_LOG_RETENTION_DAYS`，需与此处一致，不一致时拒绝启停/升级。`VLOGS_DISK_LIMIT` 是后端磁盘保留目标，非硬配额；`VLOGS_MEMORY_LIMIT` 是后端缓存额度，非进程 RSS 上限。真实容量仍需按日志量测量。

程序独立存放于 `storage/releases/<二进制SHA256>/`，`storage/current` 选择引擎；日志数据默认仍在 `data/victorialogs/`。更换平台 `current` 不更换存储程序。平台包携带相同引擎且存储配置未变时不重启 VictoriaLogs；引擎变化则先替换独立链接并检查健康，之后才切换平台。托管引擎只允许相同或更高版本，拒绝隐式降级；更新固定官方版本前仍需核对上游兼容说明并验证数据恢复。

平台启动失败时尝试恢复旧平台，保留当前存储引擎和数据。存储启动失败时返回失败、平台保持停止，不将已打开的数据目录自动交给旧引擎；修复原因后重试原升级包，或运行 `start` 检查现有选择。`rollback` 只切平台版本，不回滚数据或降低引擎版本，也不能撤销插件数据迁移。长期备份不由此命令代替。

从旧版控制命令第一次升级必须使用随新包上传的 `/tmp/devopsplatformctl`；之后包内校验通过的命令会原子安装到 `/usr/local/bin/devopsplatformctl`。新安装/升级包必须包含引擎版本元数据和受校验的管理命令，旧格式包不能直接用于新安装/升级；历史已安装平台仍可通过 `rollback` 切回，存储继续使用当前引擎。

安装/启停/升级使用文件锁互斥。发布包先在私有临时目录校验，拒绝目录穿越、软硬链接及特殊文件，校验通过后才改变服务状态。`support/victorialogs.service` 只是默认路径参考，控制命令会生成实际路径对应的单元，不需要手工复制。

### 接管已有服务与外部存储

已用旧参考单元手动安装时，仅自动接管“相同运行用户、单行直接执行 `<根目录>/current/bin/victoria-logs-prod`、明确 `Nd` 保留期、无 ExecStart drop-in 覆盖、旧二进制与包内摘要相同”的服务。原单元先备份到 `backups/`，数据目录、其他参数和 drop-in 原样保留；保留天数改由组件配置提供。原先写死的内存/磁盘参数仍在单元中，修改这些旧参数须继续维护原单元并 `systemctl daemon-reload`，再显式 `restart victorialogs`。无法确认的服务或不同旧引擎在停服前拒绝接管，避免擅自更改已有实例。

外部存储继续在 `config/devopsplatform.env` 配置：

```ini
DEVOPSPLATFORM_VICTORIALOGS_URL=https://logs.example.internal
DEVOPSPLATFORM_VICTORIALOGS_MANAGED=0
DEVOPSPLATFORM_LOG_RETENTION_DAYS=365
# 可选：DEVOPSPLATFORM_VICTORIALOGS_TOKEN=受保护代理的令牌
```

默认 `auto` 只托管 `http://127.0.0.1:9428` 或 `http://localhost:9428`；其他地址自动按外部存储处理。显式 `0` 不安装/启停/升级远端进程，只等待健康；远端保留策略自行维护且须与平台一致。自定义本机回环端口须显式设置 `MANAGED=1` 并使 `VLOGS_LISTEN_ADDR` 与 URL 一致；已有未托管进程占用地址时拒绝再启动一个实例。

单独运行日志服务时，数据面 9801 供 Agent，管理面 19801 绑定 loopback 供平台网关。只有数据面暴露给采集机器，9428 和管理 API 不对外开放。服务单元不启用 VictoriaLogs 日志删除 API。

Docker 可使用 `DEVOPSPLATFORM_DATA_TOKEN=实际令牌 docker compose -f deploy/compose.logs.yaml up -d --build`。官方依赖不暴露 9428 到宿主机，管理面只映射到 127.0.0.1；默认保留 7 天、正文磁盘目标 20GiB，队列高水位 2GiB。容量按实际写入调整；按磁盘删除是 VictoriaLogs 分区策略，可能提前结束时间保留窗口，也不是严格文件系统磁盘配额。

## 配置

- `-victorialogs-url` / `DEVOPSPLATFORM_VICTORIALOGS_URL`：默认 http://127.0.0.1:9428。不可将账号密码嵌在 URL；后端重定向不会被跟随。
- `DEVOPSPLATFORM_VICTORIALOGS_TOKEN`：可选 Bearer Token（例如受保护代理），不写日志；原始 VictoriaLogs 默认 loopback 内部使用。
- `DEVOPSPLATFORM_VICTORIALOGS_ACCOUNT_ID` / `..._PROJECT_ID`：可选专用租户。多个独立平台共用存储时必须分配不同租户；同一租户只接入一个日志服务状态目录，不把其他采集器直接写入此专用数据集。
- `-retention-days` / `DEVOPSPLATFORM_LOG_RETENTION_DAYS`：无配置时默认 7，显式命令行参数优先，必须与后端 retentionPeriod 一致。统一管理命令会向日志插件传入沿用的保留期。
- `-disk-gb`：现在仅代表**本地短期投递队列**高水位，默认 2GiB。另有 SQLite 页数上限和 WAL 预留；不是 VictoriaLogs 日志正文容量。
- `-log-replay-window`：默认 1 小时，自最近一次投递重新计算，保留压缩补发载荷。
- `-log-retry-window`：默认 48 小时，保留事件/批次去重收据。必须不短于回放窗口、不超过日志保留期。超过此时间的首次接收记录进入隔离，不默默当成新日志重放。需要更长断连容忍期时，先增加窗口和队列容量并压测。

## 接收、恢复及空间

本地 state.db 使用 WAL + synchronous=FULL，压缩投递载荷、去重键、批次收据、隔离记录同事务提交后才 ACK。相同 ID 内容冲突返回 409 且整批回滚，队列压力/写满返回 503，不删除 Agent 本地 WAL。空心跳仍可提交。坏外层 JSON 无 ACK；可解析批次中的非法时间、过大正文等记录持久隔离，其余继续接收，响应增加 quarantined 数量。

后台投递是至少一次，不承诺 VictoriaLogs 物理零重复。列表按稳定 event_key 取一条，告警、服务总数和分钟计数按唯一 event_key 统计；event_id、Trace ID 不作为 stream 标签。当前按应用/环境/主机建立 stream，平台原有服务/环境隔离继续适用。

HTTP 200 和“已可查询”不等于逐请求可靠落盘。队列保留短期回放副本，周期按**事件 ID 集合**核对，发现缺失重新投递；观察到后端重启会加快核对。若下游多次成功响应仍缺少记录，缺失记录转入隔离，保留原内容，已确认记录继续流转。超过回放窗口且本轮核对完整后才删除已发送正文，去重收据保留到重试窗口；清理后增量回收空闲页并 checkpoint，不为正常维护运行整库 VACUUM。

默认一小时回放是可配置的恢复窗口，**不是无限期备份或端到端 exactly-once / 零丢失承诺**。窗口结束后仅保留 VictoriaLogs 正文，平台不能恢复其后发生的存储损坏、磁盘丢失或超窗回退。正式使用需对 VictoriaLogs 配置独立备份；需要机器故障仍不丢时另配副本。队列也需要持久数据盘，容器重建保留卷。

队列只做投递/短期补发，不参与历史查询。去重收据仍会随窗口内事件量增长，队列容量需要覆盖回放正文、去重元数据、隔离记录及 WAL 余量；高水位会背压，不能把同一样本压缩比外推为生产容量。

状态页展示 VictoriaLogs 正文/索引指标、待投递/确认批次、短期回放批次、队列磁盘与隔离数；后端失败明确显示，磁盘未知用“—”而不是 0。/api/stats.storage 提供同样状态与窗口信息。查询不可用时返回错误，不回退到旧库，不将故障伪装为空结果。

## 隔离记录修复

管理面 `GET /api/log-quarantine` 返回最近 100 条隔离状态。管理员通过 `GET /api/log-quarantine/{id}` 查看原记录和原因，检查/修正后 `POST /api/log-quarantine/{id}/repair`，正文为 `{"confirm":true,"event":{...}}`。平台网关注入当前用户角色；这些接口不在 Agent 数据面开放。不要把自行设置的 X-Role 暴露为公网认证方式。

修复会生成明确的新 event_id、一次性入队，并保留被拒绝原记录。重复修复被拒绝；未解决隔离记录不会自动删除，修复记录保留重试窗口后清理。当前未添加专门的修复页面，管理员可经管理 API 操作。

## 正式使用前切换

新建运行只产生 state.db 和 VictoriaLogs 数据目录，不生成日志日表、FTS 或按天 SQLite 归档。旧 logs.db 只被一次性只读读取告警规则/通知配置，保持主键和作用域；**不导入历史日志，不提供旧库查询**。旧测试文件原样保留，停服务后可归档到运行数据目录外；不要在服务运行时删除或拷贝活动 WAL。本轮没有删除现有业务数据，也没有在测试服进行清理。

备份/恢复须同时覆盖 VictoriaLogs 和日志服务 state.db，保持投递身份及递增序号；不要只清空状态库后继续向原租户写入。同时间分页采用时间 + 本机持久递增序号，并冻结首屏序号上限；使用原查询范围继续翻页。断连时新接受记录可先出现在实时流，历史检索等待后台投递可见。

## 验证

`VLOGS_TEST_BINARY=/绝对路径/victoria-logs-prod go test -race -ldflags=-linkmode=external ./...` 会使用真实官方实例及独立临时租户/目录验证存储、查询和故障；未配置二进制时集成用例明确跳过，不能当作已验收。子进程崩溃 helper 在普通测试枚举中跳过，但由父测试真实调用；浏览器夹具另行显式启动。部署不依赖测试环境变量。

控制命令隔离回归：`python3 scripts/test-devopsplatformctl.py` 使用临时目录和 systemd/账号替身；指定 `CTL_VLOGS_TEST_BINARY` 与 `CTL_LOGSERVER_TEST_BINARY`（本机原生可执行文件）后，再实测真实存储启动/重启、制品替换后数据仍可查询和插件保留期。`python3 scripts/test-release-victorialogs.py` 使用编译器及官方下载响应替身验证双架构包内容和摘要失败拒绝发布。这些测试不触碰业务服务，不等于真实 Linux PID 1、低权限用户启动或跨 VictoriaLogs 版本的数据兼容验收。
