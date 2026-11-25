# Dubbo 接口执行器

平台以独立 Java 进程调用 Dubbo SDK，不通过 Telnet、不上传业务 JAR、不执行任意脚本。每次调用最多 120 秒；平台最多同时启动 4 个执行器。发现与调用分离：先解析已绑定的注册中心，冻结合法提供者地址，再按精确接口、方法和有序 Java 参数调用；failfast、retries=0。超时或连接中断记录结果未知，不自动重发。

## 构建与部署

使用 JDK 17 和 Maven 3.9：

```sh
mvn -f tools/dubbo-executor/pom.xml clean package
```

产物是 `tools/dubbo-executor/target/dubbo-executor-1.0.0.jar`。将其放在部署服务可读且普通用户不可修改的绝对路径，给 **deploy 进程**配置：

```sh
DEVOPS_DUBBO_EXECUTOR_JAR=/opt/devops/tools/dubbo-executor-1.0.0.jar
DEVOPS_DUBBO_JAVA=/usr/bin/java
```

执行器未配置时，HTTP 功能继续可用，Dubbo 调用在预检阻止。JAR 不嵌入 Go 二进制，部署时必须单独携带；当前平台打包脚本不会自动安装它。实际运行验证使用 JDK 17；编译产物为 Java 8 字节码，但不据此宣称客户端 JDK 8 已验证。

在接口测试的环境设置新增 Dubbo 入口，选择已绑定的 Nacos 资源别名或明确的 `dubbo://host:port` / `tri://host:port`。注册中心凭据从服务端资源连接读取，不放入文档、用例、命令参数或浏览器。Dubbo Group/Version 是方法身份；Nacos namespace/group 属于资源/注册中心配置，两者不能混用。

当前 Nacos 适配标准 HTTP 地址及 username/password（或无认证）；HTTPS、自定义 context path、临时访问令牌不支持。Dubbo 3 的应用级发现需要提供者正确发布元数据。SDK 内部允许读取注册/元数据，业务调用仍只发送至预检选定的地址。

## 已验证的兼容矩阵

隔离 Nacos 2.4.3：Dubbo 2.7.23（Java 8 提供者，dubbo）、Dubbo 3.3.6（Java 17 提供者，应用级注册，dubbo 和 tri）。均验证直连、注册发现、对发现地址的调用及 Long 精度。没有连接业务注册中心或调用业务服务，不表示所有 2.7/3 小版本和自定义序列化均已覆盖。

JUnit 验证真实网络上的 Long、Map、无参方法和业务异常只调用一次。`compatibility/verify.py` 是显式 opt-in 的本机隔离工具，需自行启动空白 Nacos，并编译 compatibility/pom.xml 对应版本；不能对业务注册中心运行。其默认本地 Java 路径可用 DUBBO_TEST_JAVA、DUBBO_TEST_JAVA8、DUBBO_TEST_HOST 覆盖。
