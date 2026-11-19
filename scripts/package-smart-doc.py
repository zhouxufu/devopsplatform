# -*- coding: utf-8 -*-
#!/usr/bin/env python3
# Keep UTF-8 package metadata outside macOS bash 3.2 heredoc handling.
import hashlib,json,pathlib,sys,zipfile
stage,out,system,arch=sys.argv[1:];stage=pathlib.Path(stage)
manifest={'id':'smart-doc','name':'Smart-doc 接口文档','version':'1.1.0','protocol':1,'os':system,'arch':arch,'entrypoint':'bin/smart-doc-plugin','sha256':hashlib.sha256((stage/'bin/smart-doc-plugin').read_bytes()).hexdigest(),'capabilities':['api.document.generate.v1','workflow.node.v1'],'access':{'api.document.generate.v1':'admin','workflow.node.v1':'member'},'configuration':[]}
(stage/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
(stage/'README.txt').write_text('管理员安装并启用后，部署编排的插件节点可选择 Smart-doc。\n在接口文档页选择应用和环境，按应用构建工具配置来源。Maven 填写项目已验证的固定插件版本；Gradle 复用仓库 build.gradle/build.gradle.kts 已应用的 smart-doc 插件版本，默认执行 smartDocOpenApi，也可选 子项目:smartDocOpenApi。\n配置路径相对当前构建工作目录。输出须位于 target/ 或 build/，并与 smart-doc.json 的 outPath 一致；Maven 默认 target/api-docs/openapi.json，Gradle 默认 build/api-docs/openapi.json。Gradle 传入 smartdoc.configFile 项目属性；旧版插件需要在 build.gradle 中显式读取该属性作为 smartdoc.configFile，不自动升级项目插件。\n适配器不携带 JDK/Maven/Gradle，不执行构建；平台 Runner 复用应用已有可执行文件/Wrapper、环境属性、凭据和隔离环境。\n编排生成使用已审核制品对应源码 Commit 与构建配置，在独立临时目录执行，不重新发布应用。开发、测试环境选择插件后默认构建后生成，生产默认关闭；运行详情关联快照与同环境变更历史。\n停用阻止新调用；冻结后开始的 Runner 任务按原版本完成。升级不自动重放旧计划；历史文档保留。\n')
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
 for f in stage.rglob('*'):
  if f.is_file():z.write(f,f.relative_to(stage).as_posix())
print(out)
