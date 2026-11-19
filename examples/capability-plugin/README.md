# 最小能力插件

这是一个只有标准库依赖的独立 Go HTTP 服务，实现 `example.echo.v1`，不访问业务系统。

将 `main.go` 和 `build.sh` 放在同一目录，运行：

```sh
bash build.sh
# 给 Linux amd64 测试机打包：
GOOS=linux GOARCH=amd64 bash build.sh
```

构建机需要 Go、Bash、Python 3；目标机只需要安装 ZIP 内的编译产物。在平台「插件管理」上传生成的 ZIP，可选配置 `prefix` 后启用、测试。详细 manifest、鉴权、启动环境变量、调用和生命周期见 [完整协议](../../deploy/PLUGIN-PROTOCOL.md)。

示例是管理员能力，不会自动产生业务界面。新建日志数据源应实现已有 `logs.search.v1` 契约，而不是声明 echo 后期待平台显示日志。
