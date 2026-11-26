import{p as h,m as v}from"./marked.esm-ViRc3CnZ.js";import{r as f,c as l,o as c,A as p,H as e,F as _,G as x,C as b,w as k,D as y,J as S,B as T,e as s,_ as P}from"./OptionPicker-BF84aLPd.js";import{i as O}from"./_plugin-vue_export-helper-BUiZIlGo.js";const n=`# 可安装能力插件协议 v1

本文说明当前实现。后续统一页面、能力、配置、节点、任务与流的演进方向见 [Future：全局插件协议与渐进演进](FUTURE-PLUGIN-ARCHITECTURE.md)；Future 为设计草案，不改变本文现有安装格式和接口。

状态：已实现协议 major=1、日志查询 logs.search.v1（Splunk）、规则编译 policy.release.v1、文档生成计划 api.document.generate.v1 与编排节点 workflow.node.v1。现有工作区插件 manifest.yaml 继续保留；本协议描述无页面的业务能力插件，二者共享平台登录但独立注册。禅道、数据库等后续实现自己的领域能力，不复用日志字段或厂商查询语法。


## 从零定义一个插件

1. 先选能力：接入已有日志查询就实现 \`logs.search.v1\`；接入规则编辑就实现 \`policy.release.v1\`。新领域使用新能力名和独立输入/输出契约。声明任意新能力只获得通用调用入口，**不会自动生成业务页面、数据权限或审批流程**，必须同时实现宿主消费方。
2. 编写独立 HTTP 服务，监听宿主给定的回环地址；实现下方4个接口，验证每次调用的 Bearer token。语言不限，目标机只需运行编译后的程序。
3. 在 ZIP 根目录放 manifest.json，把编译后的可执行文件放到 entrypoint 路径。入口文件 SHA-256 必须与清单一致。清单声明的 id、version、capabilities 必须与运行时 describe 一致。
4. 在插件管理上传 ZIP，按声明表单保存配置，再启用和测试。安装不自动启用；缺少业务配置不要填写虚假地址后启用。
5. 使用本文调用示例检查行为，再接入业务界面。升级先停用并保留非秘密配置、卸载旧包、上传新版、重新配置并启用；卸载会删除该插件私有配置，没有自动配置迁移。

最小项目只有 \`main.go\` 和 \`build.sh\`，均可在本页面下载。保存到同一目录，运行 \`bash build.sh\`；给测试机打包运行 \`GOOS=linux GOARCH=amd64 bash build.sh\`。开发机需 Go、Python 3 和 Bash，测试机无需安装这些工具。示例没有外部连接，不会读写业务数据；不能拿示例的 echo 能力当作日志查询。

### manifest.json 完整示例

\`\`\`json
{
  "id": "example-echo",
  "name": "示例回显插件",
  "version": "1.0.0",
  "protocol": 1,
  "os": "linux",
  "arch": "amd64",
  "entrypoint": "bin/example-echo",
  "sha256": "这里填入口文件的64位十六进制SHA-256，build.sh会自动计算",
  "capabilities": ["example.echo.v1"],
  "access": {"example.echo.v1": "admin"},
  "configuration": [{"key":"prefix","label":"回复前缀","type":"string","required":false}]
}
\`\`\`

| 字段 | 定义与限制 |
|---|---|
| id | 稳定插件标识，小写字母开头，仅小写字母、数字、短横线，长度1～63；安装目录和路由使用它 |
| name / version | 显示名称 / 插件版本，非空，分别不超过100 / 60字节；版本需与describe完全一致 |
| protocol | 基础协议主版本，目前只能是1；领域能力版本另放在capabilities中 |
| os / arch | 必须匹配宿主，例如linux/amd64、darwin/arm64；与构建机平台可不同 |
| entrypoint | ZIP内相对路径，不可绝对路径、越界、符号链接或包含反斜杠/冒号 |
| sha256 | 入口程序内容的SHA-256，小写64位十六进制；其余文件没有签名担保 |
| capabilities | 1～20个不重复能力名，例如logs.search.v1；只能声明实际实现的能力 |
| access | 每个能力可选member或admin，省略默认admin；不代替领域权限和审批 |
| configuration | 最多40个字段；key唯一、小写字母开头的小写字母数字/短横线/下划线，最长64；type只支持string、secret、json；label最多256字节，required表示启用前必须填写 |

ZIP最多64 MiB、32个目录/文件条目，解压总量最多96 MiB，manifest.json最多64 KiB。entrypoint之外的配置资源也可入包，必须在这些限制内；插件不会收到额外启动参数，不使用脚本里的硬编码端口。

### 启动时注入的环境变量

| 变量 | 作用 |
|---|---|
| PLUGIN_LISTEN_ADDR | 宿主分配的127.0.0.1地址与临时端口，插件绑定此地址 |
| PLUGIN_AUTH_TOKEN | 每次进程启动随机生成；每个接口必须恒定时间比对Authorization: Bearer，不打印或回传 |
| PLUGIN_CONFIG_PATH | 宿主创建的0600 JSON配置文件绝对路径；自行读取声明字段，不把文件内容写到日志 |

示例收到SIGTERM/SIGINT会停止接收请求并在3秒内退出；宿主停用时会取消请求并最多等待5秒再强制结束。启动握手必须5秒内就绪；每实例最多4个并发请求；宿主调用超时25秒、响应上限2 MiB。耗时操作必须监听请求取消，不在断开后继续执行；首版不支持流式传输。

### 四个接口的精确响应

所有接口必须验证Bearer。describe/health为裸JSON；test/invoke建议统一data/error包装；运行时兼容旧test的 \`{ok:true}\`，新插件应使用统一包装。

\`\`\`text
GET /v1/describe
{"protocol":1,"id":"example-echo","version":"1.0.0","capabilities":["example.echo.v1"]}

GET /v1/health
{"ok":true}

POST /v1/test
{"data":{"ok":true}}

POST /v1/invoke
请求：{"request_id":"demo-1","capability":"example.echo.v1","operation":"echo","actor":{"user":"admin","role":"admin"},"input":{"text":"hello"}}
成功：{"data":{"text":"hello"}}
业务失败：{"error":{"code":"invalid_argument","message":"输入无效","retryable":false}}
\`\`\`

业务错误通过HTTP200里的error传递；Token缺失/错误返回HTTP401，未知路由404。平台只转发HTTP200 JSON，其他状态显示插件连接异常。不要把外部系统的401直接冒充平台登录过期。actor来自宿主可信登录会话，插件不要从input读取“管理员”或伪造审批；必需的业务范围仍由插件校验。

### 安装与调用最小示例

编译出ZIP后，在平台插件管理上传、配置prefix（可留空）、启用并测试。以下在已登录平台的同源浏览器开发者工具中运行，只调用回显示例：

\`\`\`javascript
const response = await fetch('/api/extensions/example-echo/invoke', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({request_id:'demo-1',capability:'example.echo.v1',operation:'echo',input:{text:'hello'}})
});
console.log(await response.json());
// {data:{text:'hello'}}；若配置prefix，text会带该前缀。
\`\`\`

浏览器不传actor、不接触进程token；平台覆盖actor。示例是管理员能力，成员不能调用。验收至少检查：错误token被拒绝、describe和清单一致、缺必填配置不能启用、错误能力/操作返回unsupported、响应不含秘密、超限返回错误、取消/停用不再工作、重启恢复、卸载不修改外部数据。示例不自动装到测试机，避免与实际业务插件混淆。

## Splunk 安装后怎么配置

测试机的Splunk插件可先安装保持未启用。需要的是**REST API根地址**（常见为 \`https://主机:8089\`），不是 \`/zh-CN/app/search/search\` 网页地址。不能根据网页端口8888直接断定API端口和协议；需由现场确认实际路由。

- endpoint：实际可达的REST根地址，不带网页路径、用户信息、查询参数或片段。
- username / password：只读查询账号，密码仅在管理员表单填写，不发在聊天、manifest或示例中。
- users：允许使用该数据源的**平台账号**，例如 \`["admin"]\`，不是Splunk账号列表。
- bindings：明确每个应用+环境映射的index，可加sourcetype和固定字段filters；共享index请配置真实应用和环境过滤，不能照抄字段名。
- fields：真实日志字段名到平台level/trace_id/host/path/message的映射；不配置的level/trace_id不会凭空补齐。

\`\`\`json
[{"service":"estate-ai-seek","environment":"test","index":"请替换为真实索引","filters":{"service":"estate-ai-seek","env":"test"}}]
\`\`\`

上例的service/env只是结构演示，**需要替换为现场真实字段和取值**。不要保存占位索引。先保存、启用、测试连接，再到日志查询选择Splunk数据源，明确应用、环境及24小时内时间范围；使用字面量关键词。连接测试成功只证明登录和server/info可访问，不代表index权限/字段映射已正确。历史查询的失败/截断显式展示，不会悄悄回退本地。

## 边界

平台管理安装包、实例配置、进程、身份、能力路由、限流与取消。插件拥有厂商协议、认证会话、连接测试、字段映射和分页状态。平台不导入厂商包，不执行插件返回的脚本/HTML。文档提供方返回受约束的任务/属性计划，由现有 Runner 按已授权工具链执行；异步节点使用领域执行器或只读任务协议。插件故障不回退其他数据源；未查询和未知数据不等于零或健康。

平台与独立进程通过仅回环 HTTP/JSON 通信，插件语言不限。进程通过 PLUGIN_LISTEN_ADDR、PLUGIN_AUTH_TOKEN、PLUGIN_CONFIG_PATH 获取启动配置。每次启动使用随机 token；所有接口校验 Authorization: Bearer。token 不进入浏览器/manifest/命令行。插件与平台同操作系统用户，属于受管理员信任的本机代码，并非沙箱；安装不自动启用。

## 安装、实例与生命周期

ZIP 根目录 manifest.json，含 id/name/version/protocol、os/arch、entrypoint、sha256、capabilities、configuration 字段描述。首版一个安装包对应一个实例；厂商插件可在配置内绑定多个应用环境。声明的能力名带领域版本，如 logs.search.v1；基础协议升级与领域能力升级分别兼容。access 按能力声明 member/admin，缺省只允许管理员；请求 actor 始终由平台会话覆盖。后续写能力还需接入对应领域的审批/幂等协议，admin 角色不是绕过业务审核的凭据。拒绝不支持的 major、路径穿越、符号链接、重复文件、超额文件、错误架构/摘要、非回环运行地址。安装包不会自带真实凭据。

状态 installed/starting/running/stopping/stopped/error。安装后管理员保存配置、启用；启动仅在 describe/health 就绪并匹配声明后开放调用。停用先撤销入口并取消所有在途请求，等待退出后强制结束；其他插件保持运行。服务重启恢复已启用配置。卸载要求先停用，删除其包/私有配置，不操作外部系统或其他实例。升级首版为停用、卸载并安装新版；不宣称无缝升级或多版本共存。

每次启动的 generation 改变，旧分页游标失效，不把旧请求路由到新实例。平台固定超时、单实例并发上限、响应体上限；HTTP 请求取消沿链路传播。旧响应不能覆盖新配置/新数据源。

## 通用接口

- GET /v1/describe：{protocol:1,id,version,capabilities}，配置 schema 在安装 manifest，首版字段类型 string/secret/json，JSON 字段解析为结构化值。
- GET /v1/health：{ok:true}。
- POST /v1/test：只验证连接/配置，返回 {ok:true} 或统一错误，不回传凭据。
- POST /v1/invoke：{request_id,capability,operation,actor:{user,role},input:{...}}；actor 由宿主会话注入，不能接受浏览器伪造。
- 成功：{data:{...}}；失败：{error:{code,message,retryable}}。代码 invalid_argument/unauthenticated/permission_denied/unsupported/unavailable/timeout/cursor_expired/resource_exhausted/internal。错误、日志、API 响应不含密码、token、原始响应或配置秘密。

平台管理 API 位于 /api/extensions：GET（用户只看可用描述；管理员查看管理状态）、POST multipart 安装包、PUT /{id}/config、POST /{id}/enable|disable|test、DELETE /{id}。管理仅管理员；调用 POST /{id}/invoke 注入身份并检查声明能力。配置 API 只返回非 secret 字段及 configured_secrets 名称；不把秘密返给浏览器。空白 secret 表示保留既有值。配置及包目录 0700，配置文件 0600；运行日志丢弃，避免第三方直接打印秘密。

## 日志能力 logs.search.v1

操作 search，输入 {service,environment,start,end,host,levels:[],text,limit,cursor}。UTC 毫秒时间，必选应用/环境，最多24小时；limit 1..200，结果按时间降序。text 是字面量子串，不是 SPL、SQL 或平台本地全文语法；供应商不支持的语义须明确报错。首版页面在外部数据源模式展示字面量提示，保留本地查询原语义。后续结构化过滤操作符需另声明能力，不假装不同搜索引擎语义一致。

输出 {hits:[{event_id,ts,service,environment,host,path,level,trace_id,message}],next_cursor,has_more,complete,truncated}。未知总数不发送 count。cursor 由插件管理，不暴露 SID/偏移；绑定查询、用户、配置及 generation，超时/停用后明确失效。

Splunk 首版配置显式定义允许使用的 users、应用/环境到 index/sourcetype 的绑定、不可由查询覆盖的 filters 字段等值约束及字段映射。共享索引必须配置实际应用/环境过滤；省略 filters 表示管理员声明该 index/sourcetype 整体属于该绑定，插件不能自行判断索引内数据归属。必须命中服务端绑定，不能通过 text 覆盖 index 或注入管道；不提供任意 SPL 执行入口。账号采用旧版 Session 登录，查询通过 jobs/poll/results，最多截取1000条分页，停止/过期时清理临时搜索任务。不读写 Splunk 日志数据/配置，不复用网页 Cookie。没有 trace_id 时不制造调用链数据。

仅实现历史查询；外部 Live、统计、告警、源应用自动发现能力未声明，保持本地能力独立。原有实时日志入口始终可见，其页面明确标注本地采集；历史数据源选择不会隐藏或替换 Agent→本地存储→WebSocket 的实时链路。安装后尚未配置不进行外部网络访问。真实 Splunk 6.6.3 联调与本地模拟协议测试分别记录，测试成功不代表真实实例已验证。

## 构建与安装示例

\`\`\`sh
./scripts/package-splunk-plugin.sh
GOOS=linux GOARCH=amd64 ./scripts/package-splunk-plugin.sh
\`\`\`

产物 dist-plugins/splunk-logs-<os>-<arch>.zip。平台顶部「插件管理」上传，按包内 README 配置，保存后启用，再测试连接。日志查询选择对应数据源。此管理入口只管理外部能力插件，已有 logs/deploy/research 等工作区模块不在这里卸载。

允许用户示例 \`["admin"]\`（无登录本地模式为 \`["local"]\`）；绑定示例 \`[{"service":"order-api","environment":"prod","index":"main","sourcetype":"app","filters":{"service":"order-api","env":"prod"}}]\`；字段示例 \`{"level":"severity","trace_id":"traceId"}\`。字段默认 host→host、path→source、message→_raw，级别和 trace 缺省不伪造。凭据留空保留，卸载删除该实例私有凭据。管理员停用修改配置后再启用；第一版不支持在运行时替换配置。

验证命令：\`go test ./internal/extensions ./internal/splunkplugin ./internal/platform ./internal/server\`，可加 \`-race\`。macOS 本机使用 \`CGO_ENABLED=1 go test -ldflags='-linkmode=external' ...\`。\`scripts/serve-extension-fixture.py\` 在 127.0.0.1:19970/19971 创建独立临时数据目录，真实启动能力插件进程，模拟 Splunk 6.6.3 的认证、任务及分页接口；它不访问用户线上实例。

## 实时日志的兼容与后续能力

本地实时链路保留现有 Agent ingest ACK/去重、Hub 过滤、WebSocket 最近日志回放和实时推送。数据源插件不接管 Agent 接收端或本地存储。\`logs.search.v1\` 的启停不关闭本地实时连接；实时页面的选择和历史查询的数据源分别解释。

后续外部实时能力单独使用 \`logs.stream.v1\`，必须定义开始订阅、获取有界事件批、取消、心跳、断点游标和明确的 gap/截断事件。不能仅把历史 search 定时重跑后就宣称支持无遗漏实时日志。该能力本轮只预留设计，未声明到 Splunk 插件中；页面仍提供原本的本地实时日志。

## 上线识别规则能力 policy.release.v1（2026-10-20）

本轮新增独立的 \`release-rules\` 编译插件，沿用安装包、握手、身份注入、限流、停用和卸载协议；无凭据、无实例 JSON 配置。它不读取业务数据库、不访问禅道，不执行发布。源码在 cmd/rules-plugin、internal/rulesplugin，通用数据契约与解释器在 pkg/releasepolicy，厂商适配仍由当前来源连接读取模块提供。本轮没有将整个禅道模块搬到外部进程。

操作 schema 返回版本1及标准字段；compile 输入 \`{policy:{version:1,default,rules,required_fields?}}\`，返回 \`{policy:...}\`。规则包含 name、match(all/any)、conditions、decision；条件字段 kind/product/project/status/resolution/severity/priority/title，操作 in/not_in/contains（contains 仅标题，区分大小写）。最多100条规则，每条8个条件，每条件50个值；只接收有界声明式数据，不能返回 SQL、脚本、HTML 或任意字段表达式。未来新增领域字段/表达式需明确能力版本，不透传厂商代码执行。

平台网关在保存和预览前，用真正管理员身份调用选中的编译器，停用/故障/拒绝则阻止新编译，不静默改用其他提供方。编译输出由交付域再次校验，保存规则、提供方ID/版本及规则 revision；修改使用 CAS 并审计。内置提供方使用相同契约，可由管理员明确选择，不能在外部插件失败时偷偷替换。

保存后的规则由平台确定性解释器执行，并生成 SQLite/MySQL 的数据筛选表达式；实时来源列表、详情和版本候选使用相同的三值判定语义。规则按顺序匹配；此前规则缺字段且无法证明不匹配时，保持 unknown，不落入后续否定规则/默认无需上线。人工覆盖始终优先；禁用自动识别不会清除人工覆盖。停用/卸载编译插件仅停止新的编译，已保存的规则仍可执行，因此不会改变冻结交付范围或已有配置。这是编译插件，不是每行远程执行的规则引擎。

旧项目规则读取时转换为条件，保留“缺项目待判定”的前置条件；新配置默认待人工确认。首次保存新结构后不再维护旧 projects 数组，历史记录不删除。可在界面明确取消旧前置条件，改为只检查每条规则所需字段。来源名称和编码分离，修复方案不猜测 fixed2 等现场编码含义。选项读取失败时显示失败状态，可重试或输入人工核对的编码。

配置路径：研发工作台 → 设置 → 上线识别规则。选择提供方，按“字段→条件值→结果”配置，再用当前来源 Bug 编号预览。预览只读，受产品范围与来源权限约束，展示命中规则及人工覆盖后的有效结果；不导入 Bug、不保存判定、不执行交付。发布状态和运行健康不由本规则推断。

构建：\`./scripts/package-rules-plugin.sh\`；Linux 包使用 \`GOOS=linux GOARCH=amd64 ./scripts/package-rules-plugin.sh\`。在插件管理上传 dist-plugins/release-rules-<os>-<arch>.zip，启用后在规则提供方选择“通用上线规则编译器”。普通用户无需安装插件，管理员可先用内置条件编辑器。

## 编排节点扩展 workflow.node.v1 与文档生成 api.document.generate.v1（2026-10-22）

编排引擎提供通用的 \`extension\` 节点，节点类型列表通过 \`GET /api/workflow-node-providers\` 读取已安装、已启用插件的声明。安装不自动启用或执行，停用插件后不能新选/新预览，已有模板保留绑定并显示不可用。界面中的“刷新插件节点”无需重新加载编排草稿。平台不按 smart-doc 名称写死菜单。

### 节点发现与安装身份

插件 manifest 声明 \`workflow.node.v1\`，实现 invoke 的 \`describe\` 操作。例如 smart-doc 返回：

\`\`\`json
{"version":1,"key":"api-documentation","name":"生成接口文档","executor":"api.document.generate.v1","configuration":"application-binding"}
\`\`\`

首版每插件声明一个节点；\`key\` 在插件内稳定，允许小写字母、数字、下划线和短横线。显示名称可修改。宿主的工作区模块与无页面能力插件仍是不同注册表。接口中心是独立页面，启停生成插件不卸载接口中心或删除文档。

Manager 为成功调用附加 \`provider:{id,version,digest}\`，来自实际安装清单；插件回传同名值会被覆盖。Request 可声明 \`expected_version\`、\`expected_digest\`，Manager 在取得进程实例时核对，不匹配返回 \`version_mismatch\`。编排预检固定插件包摘要、版本、节点声明、参数、应用/环境和制品范围；执行前再次核对，不把新安装包替换进已审核执行单。

供可信后端使用的回环 broker 仅开放宿主 allowlist 中的能力，注入服务身份，配置文件 0600、目录 0700；不在浏览器、manifest、命令行、构建快照或日志中传递 Token。它不是公开的权限提升接口。当前 delivery-core 获得文档生成/编排节点能力，不能借此调用日志或其他管理能力。

### 通用异步插件任务

不依赖特定厂商的节点可返回：

\`\`\`json
{"version":1,"key":"check","name":"只读检查","executor":"workflow.remote-job.v1","configuration":"node-fields","effect":"read-only","fields":[{"key":"rule_ref","label":"规则引用","type":"string","required":true}]}
\`\`\`

该执行协议首版只开放声明为只读的任务；不开放第三方 SQL/配置/发布写执行。写扩展仍需对应领域的审核、幂等和不确定结果处理契约。已安装进程是管理员信任的本机代码，并非沙箱，声明不等于操作系统层面隔离。

配置字段最多20个字符串字段，key 唯一、值最多2048字节；使用已有资源/凭据的引用，不录入密码/Token。预览按声明校验必填项和未知字段。实现下列 invoke 操作，单次调用仍遵守25秒限制：

- \`job.start\`：输入 \`{execution_id,node_key,environment,targets:[{service,application_id,artifact_id,artifact_digest}],parameters:{...}}\`。\`execution_id\` 是宿主生成的稳定幂等键；重复键必须返回同一任务。真正的任务在插件内部异步运行，不能在启动请求中等待完整执行。
- \`job.status\`：使用同一输入按 execution_id 只读核对；不得启动或重放任务。
- 响应 \`{execution_id,state,message?}\`；state 为 queued/running/success/failed/canceled/unknown，message 最多4096字节且不含秘密。标识必须与请求一致。
- \`describe\` 可允许 member，\`job.start\` 必须校验宿主服务身份/管理员角色；不能因为同一能力允许成员读取描述，就允许成员绕过编排执行入口调用 start。

宿主先写入 durable executing 标记，再调用一次 start。启动/状态请求结果不确定、超时、停用或中断时保持待核对，不自动重发 start；“单步执行 / 核对”仅调用 status。插件应持久化幂等记录和任务状态；丢失记录返回 unknown。当前最多等待40分钟，编排协调器等待有租约的活动任务，不把缺少结果当作成功。成功只表示该节点任务完成，不自动构成应用健康或发布验证证明。

### 文档生成计划与 Runner

smart-doc 同时声明 \`api.document.generate.v1\`。其 \`schema\` 操作返回应用配置字段，\`plan\` 输入 \`{tool,settings}\`，返回：

\`\`\`json
{"version":1,"provider":"smart-doc","provider_version":"1.0.0","tool":"maven","tasks":["com.github.shalousun:smart-doc-maven-plugin:2.7.3:openapi"],"properties":{"configFile":"./src/main/resources/smart-doc.json","file.encoding":"UTF-8"},"input_files":["src/main/resources/smart-doc.json"],"output_file":"target/api-docs/openapi.json","format":"openapi-json"}
\`\`\`

2.7.3 仅为固定版本格式示例，必须填写项目已经验证的实际版本；平台不自动升级工具链。ProviderVersion 是平台适配插件版本，与 Maven/Gradle 工具插件版本独立。宿主还会在计划中注入已验证安装包 \`provider_digest\`。smart-doc 适配器 1.1.0 支持 Maven 和 Gradle，复用同一任务/属性计划。配置文件路径相对应用当前构建工作目录；Maven 从构建根 POM 生成，Gradle 使用仓库已应用的插件。

#### Gradle 配置（适配插件 1.1.0）

\`schema\` 可接收既有输入 \`{"tool":"gradle"}\`，返回当前工具适用的配置字段，不再显示 Maven 版本。\`plan\` 返回 \`tool=gradle\`、\`tasks=["smartDocOpenApi"]\`（可配置 \`子项目:smartDocOpenApi\`）和 \`properties.org.gradle.project.smartdoc.configFile=./仓库内配置文件\`。Runner 继续复用应用已有 Gradle/Wrapper、JDK、容器、Runner 和环境属性；此 Gradle 系统属性命名空间会作为项目属性传入，无需新增协议字段。冲突的同名原 \`-P\` 配置只在文档任务副本中替换，其他应用属性不变。

仓库须在 \`build.gradle\`/\`build.gradle.kts\` 中应用 smart-doc Gradle 插件并固定版本；平台不自动修改或升级仓库插件。下例中的 4.0.0 是本次受控验证版本，已有项目沿用自身验证过的版本：

\`\`\`groovy
plugins {
    id 'java'
    id 'com.github.shalousun.smart-doc' version '4.0.0'
}
smartdoc {
    // 兼容未内置命令行覆盖的旧插件；支持按环境选择配置。
    configFile = rootProject.file(project.findProperty('smartdoc.configFile') ?: 'src/main/resources/smart-doc.json')
}
\`\`\`

平台默认读取 \`build/api-docs/openapi.json\`，smart-doc JSON 的 \`outPath\` 对应 \`./build/api-docs\`。子项目任务也必须输出到当前构建目录内所配置的 \`build/\` 或 \`target/\` 路径，不猜测多个子模块的最新文件。开发/测试按环境保存来源并默认构建后生成，生产默认关闭；生成失败的阻断策略、插件摘要冻结、快照、制品关联及同环境基线对比沿用原逻辑。退出码为 0 但没有新文档同样视为失败。

插件只生成有界声明式计划，不回传 shell/HTML；宿主校验任务、属性、输入/输出路径。输出必须是 target/ 或 build/ 内的普通新文件，拒绝路径越界和符号链接，执行前清除旧输出，防止“命令成功但没有新文档”误入库。smart-doc.json 的 outPath 必须与配置的输出目录一致。

接口中心 → 接口文档 → 选择应用与环境 → 文档来源：按 application_id + environment 绑定提供方、配置路径、输出路径和固定工具版本，各环境独立修订。选择插件后，development/test 用途的环境默认开启“构建时自动生成”；其他环境默认关闭，可显式调整。旧应用级来源作为配置初值，环境内首次保存建立独立修订。接口访问地址、机器和凭据仍复用现有绑定，不复制进文档配置。

配置自动生成后，新构建必须明确选择目标环境；不推断 Git 分支就是某个环境。生成作为可见的独立构建阶段“生成接口文档 · 提供方 · 环境”在制品形成后、发布就绪前执行，保存对应环境、制品和配置快照。生成失败时，必需文档阻止就绪，可选文档显示 warning 阶段。高级构建页展示自动生成环境，运行详情可以进入本次文档与变更历史。生产环境默认不自动生成。

编排文档节点要求选中的制品来自有完整源码/配置快照的构建。预检固定原制品 ID/摘要、完整 Commit、应用 ID、原工具链与计划。执行时在独立临时工作目录检出该 Commit，复用原构建工具选择、Profiles、Runner、隔离容器和重新授权的资源引用；不重新发布应用、不修改原源码工作目录。当前配置不能替代已审核快照。生成后验证实际 HEAD 与原 Commit、无已提交源码漂移，校验 OpenAPI JSON 后建立不可变快照。

任务启动后使用已固定计划完成；停用插件不强制杀死已交给 Runner 的任务。后续新调用拒绝，文档历史保留。原构建重试若插件版本/安装包变化则拒绝，须按当前配置重新提交。

### 文档消费方与边界

独立接口文档页面支持已有应用、文档快照选择、HTTP/SSE 类型识别、嵌套参数/本地 schema 引用、响应定义、原始操作定义，以及管理员手动导入 OpenAPI 3.0/3.1 JSON（最多8 MiB）。不抓取外部 \`$ref\`，不渲染来源 HTML。手动导入明确为未关联制品，不冒充当前发布版本。相同应用/环境/构建/制品/内容重复导入幂等；无历史快照修改或删除 API。稳定应用身份迁入平台目录时，文档关联在同一事务内迁移。

本轮文档解析是有界结构检查，不宣称完整 OpenAPI 规范验证器。文档插件阶段的 SSE 仅识别文档结构；HTTP/SSE 调试及前置 Token 的后续第一版已实现，参见 [接口测试说明](API-TESTING.md)。多步用例与步骤变量传递已实现。文档版本按同应用、同环境、较早快照比较，默认基线在生成时固定为上一份快照。记录新增/删除/修改/未变接口，并展示请求/响应结构、说明、本地共享 schema 及认证声明的字段变化。对比是结构变化说明，不宣称自动判定所有破坏性兼容变化。外部 \`$ref\` 不联网解析；最多 500 个接口明细、每接口 100 个字段、明细约 1 MiB，超限保留完整计数并标识截断。旧快照保留 ID 和内容，环境未知的记录保持未区分环境。Dubbo 不接入。

\`\`\`sh
GO_BIN=/绝对路径/go ./scripts/package-smart-doc-plugin.sh
GO_BIN=/绝对路径/go GOOS=linux GOARCH=amd64 ./scripts/package-smart-doc-plugin.sh
\`\`\`

产物为 \`dist-plugins/smart-doc-<os>-<arch>.zip\`。管理员在插件管理上传、启用；在接口文档按应用与环境绑定来源，在部署编排“节点类型”选择该插件。无需为每个节点重复填写项目配置。

### 已核对的旧版 smart-doc 边界

在独立合成 Java 工程中使用 JDK 17、Maven 3.6.1、smart-doc Maven 插件 2.4.6 实际执行 \`openapi\` 成功，生成物已作为 \`pkg/apidoc/testdata/smart-doc-2.4.6.json\` 回归样本由平台解析。样例保存在 \`artifacts/api-docs/real-smartdoc\`，不包含真实项目或凭据。这只证明该组合能够生成并接入文档，不代表所有业务项目和 JDK 版本都已验证。

该旧版样例即使声明 \`produces=text/event-stream\`，生成的响应 MIME 仍为 \`*/*\`；平台保留原文，不猜测 SSE。只有文档明确声明 \`text/event-stream\` 时才显示 SSE。文档与插件节点阶段的限制如上；后续已新增 HTTP/SSE 调试及 Token 前置请求第一版，参见 [接口测试说明](API-TESTING.md)，多步测试场景尚未实现。
`,G=`// Minimal independent plugin. Standard library only; no platform imports.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const id = "example-echo"
const version = "1.0.0"
const capability = "example.echo.v1"

func main() {
	address, token := os.Getenv("PLUGIN_LISTEN_ADDR"), os.Getenv("PLUGIN_AUTH_TOKEN")
	if address == "" || token == "" {
		os.Exit(1)
	}
	var config struct {
		Prefix string \`json:"prefix"\`
	}
	raw, err := os.ReadFile(os.Getenv("PLUGIN_CONFIG_PATH"))
	if err != nil || json.Unmarshal(raw, &config) != nil {
		os.Exit(1)
	}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { json.NewEncoder(w).Encode(v) }
		fail := func(code, message string) {
			reply(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": false}})
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/describe":
			reply(map[string]any{"protocol": 1, "id": id, "version": version, "capabilities": []string{capability}})
		case r.Method == "GET" && r.URL.Path == "/v1/health":
			reply(map[string]bool{"ok": true})
		case r.Method == "POST" && r.URL.Path == "/v1/test":
			reply(map[string]any{"data": map[string]bool{"ok": true}})
		case r.Method == "POST" && r.URL.Path == "/v1/invoke":
			var req struct {
				Capability string \`json:"capability"\`
				Operation  string \`json:"operation"\`
				Actor      struct {
					User string \`json:"user"\`
					Role string \`json:"role"\`
				} \`json:"actor"\`
				Input struct {
					Text string \`json:"text"\`
				} \`json:"input"\`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
				fail("invalid_argument", "Invalid JSON")
				return
			}
			if req.Actor.Role != "admin" {
				fail("permission_denied", "Administrator required")
				return
			}
			if req.Capability != capability || req.Operation != "echo" {
				fail("unsupported", "Unsupported capability or operation")
				return
			}
			if len(req.Input.Text) > 1000 {
				fail("invalid_argument", "Text exceeds 1000 bytes")
				return
			}
			if r.Context().Err() != nil {
				return
			}
			reply(map[string]any{"data": map[string]string{"text": config.Prefix + req.Input.Text}})
		default:
			http.NotFound(w, r)
		}
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
`,I=`#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
plugin_os="\${GOOS:-$(go env GOOS)}"
plugin_arch="\${GOARCH:-$(go env GOARCH)}"
plugin_stage="$(mktemp -d)"
trap 'rm -rf "$plugin_stage"' EXIT
mkdir -p "$plugin_stage/bin" dist
if [ "$plugin_os" = darwin ];then
 CGO_ENABLED=1 GOOS="$plugin_os" GOARCH="$plugin_arch" go build -trimpath -ldflags='-s -w -linkmode=external' -o "$plugin_stage/bin/example-echo" main.go
else
 CGO_ENABLED=0 GOOS="$plugin_os" GOARCH="$plugin_arch" go build -trimpath -ldflags='-s -w' -o "$plugin_stage/bin/example-echo" main.go
fi
python3 - "$plugin_stage" "$plugin_os" "$plugin_arch" <<'PY'
import pathlib,json,hashlib,sys,zipfile
root=pathlib.Path(sys.argv[1]);system,arch=sys.argv[2:]
manifest={'id':'example-echo','name':'示例回显插件','version':'1.0.0','protocol':1,'os':system,'arch':arch,'entrypoint':'bin/example-echo','sha256':hashlib.sha256((root/'bin/example-echo').read_bytes()).hexdigest(),'capabilities':['example.echo.v1'],'access':{'example.echo.v1':'admin'},'configuration':[{'key':'prefix','label':'回复前缀','type':'string'}]}
(root/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
out=pathlib.Path('dist')/('example-echo-'+system+'-'+arch+'.zip')
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
 for p in root.rglob('*'):
  if p.is_file():z.write(p,p.relative_to(root).as_posix())
print(out)
PY
`,E={"aria-label":"插件开发规范",class:"plugin-guide"},A={class:"guide-navigation"},w={class:"guide-downloads"},j=["href"],N=["href"],H=["href"],L=["innerHTML"],C={__name:"ExtensionGuide",setup(R){const t=f("start"),d={start:"快速开始",all:"完整协议",splunk:"Splunk 配置",workflow:"编排与文档插件"},u=l(()=>t.value==="start"?n.slice(n.indexOf("## 从零定义一个插件"),n.indexOf("## Splunk 安装后怎么配置")):t.value==="workflow"?n.slice(n.indexOf("## 编排节点扩展 workflow.node.v1")):t.value==="splunk"?n.slice(n.indexOf("## Splunk 安装后怎么配置"),n.indexOf("## 边界")):n),m=l(()=>h.sanitize(v.parse(u.value))),a=r=>"data:text/plain;charset=utf-8,"+encodeURIComponent(r);return(r,o)=>(c(),p("section",E,[e("div",A,[(c(),p(_,null,x(d,(g,i)=>b(s(P),{key:i,"aria-pressed":t.value===i,class:T({active:t.value===i}),onClick:M=>t.value=i,variant:"outline",size:{}.default||"default",type:"button"},{default:k(()=>[y(S(g),1)]),_:2},1032,["aria-pressed","class","onClick"])),64))]),e("div",w,[e("a",{href:a(s(n)),download:"PLUGIN-PROTOCOL.md"},"下载协议文档",8,j),e("a",{href:a(s(G)),download:"main.go"},"下载示例源码 main.go",8,N),e("a",{href:a(s(I)),download:"build.sh"},"下载打包脚本 build.sh",8,H)]),o[0]||(o[0]=e("p",{class:"guide-hint"},"示例两份文件放在同一目录，运行 bash build.sh 即可生成安装包。示例只做回显，不连接业务系统。",-1)),e("article",{innerHTML:m.value,class:"protocol-content"},null,8,L)]))}},$=O(C,[["__scopeId","data-v-9e5e42da"]]);export{$ as default};
