export const QUERY_HELP = `日志查询语法
级别下拉框支持多选，例如 ERROR + WARN；清空表示全部级别。级别筛选与下方查询条件同时生效。
空格 / AND（同时满足）：timeout nacos ｜ timeout AND nacos
OR（满足任意一个）：timeout OR refused
NOT / -（排除条件）：timeout NOT heartbeat ｜ timeout -heartbeat
双引号（精确短语）："connection refused"
括号（控制优先级）：(timeout OR refused) AND NOT heartbeat
后缀 *（匹配词前缀）：Timeout* 可匹配 timeout、TimeoutException

字段过滤
service/app：service:order-api ｜ app:order-api
env/environment：env:test ｜ environment:test
host：host:node-01
level：level:(ERROR OR WARN)
path：path:/var/log/order/*
trace：trace:4bf92f3577b34da6
message：message:"connection refused"

组合示例
service:order-api env:test level:(ERROR OR WARN) (timeout OR refused) -heartbeat

AND / OR / NOT 必须大写。仅支持后缀 *，不支持 *timeout、*timeout* 和 ?。`
