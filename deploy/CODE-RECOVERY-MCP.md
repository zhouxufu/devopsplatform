# 本地 Agent 处理代码冲突并恢复原交付

平台尝试正常合并；冲突后保留原任务和冻结范围。本地 Agent 在独立 worktree 解决并测试，通过正常 Git 授权推送；平台重新核验远端，再恢复原任务。MCP 不接受补丁、不执行客户端提供的 shell，不在平台提供在线冲突编辑器。

## 工具契约

以下为逻辑工具名，客户端调用实际命名空间。

- `get_code_conflict(preparation_id, environment)`：只读，需本人验证 Key、`builds:read`、跨应用 `*` 和对应环境授权。返回 `expected_revision`、`scope_digest`、原交付地址、仓库索引、冻结基线/目标/源提交、冲突路径、是否可恢复及禁用原因。仓库 URL 去除 userinfo/query/fragment，连接指纹不返回。完整文件从本地冻结提交读取，不是平台回传源码。
- `resume_code_preparation(preparation_id, environment, expected_revision, scope_digest, target_commits)`：需 `delivery:prepare`，仅非受保护开发/测试；生成 10 分钟冻结预览，不立即写任务或执行。`target_commits` 是按 `repository_index` 顺序排列的每个仓库解决后目标分支完整 SHA（40 或 64 位小写十六进制），缺项、分支名或 HEAD 不接受。
- 展示预览并取得针对此预览的用户同意后调用 `confirm_platform_action`；确认重新检查权限、环境与原任务基线，网关必须验证任务修订和目标提交列表，随后委托原恢复接口。保持原 ID、范围、审核、CAS 和租约，不创建第二张任务。
- `get_platform_action` 查询原 confirmation_id 的回执；`get_code_preparation` 查询原任务状态。超时、断连或 unknown/uncertain 只查询，不自动重新确认、推送、构建或发布。

本地读取和重现合并保持返回的 `sources` 顺序，不把最新源分支替代冻结提交。对独立发布范围，冲突解决应落在合并提交中，额外普通业务提交仍按既有规则拒绝；不能把 Git 包含关系当成唯一的范围审阅证据。推送沿用 Git 权限，不使用平台仓库凭据，不强推。

## 远端验证与恢复

恢复 worker 要求远端目标头与本次确认的 SHA 相同，再校验冻结基线、各源提交和既有独立范围规则，随后按冻结配置构建。已经 merged 的仓库只能确认它原来的 result_commit，且在 MCP 合并恢复路径重新核验；building 恢复只接续原冻结构建，不重放已合并代码，也不能更换构建结果提交。网页未提交 target_commits 时保留原恢复契约，不增加字段或操作。

父准备单、待办与代码任务共用上一轮完成的 blocked/running 同步逻辑。成功后保留后续人工节点，不自动执行部署、SQL 或配置。

平台随包分发 `devops-code-recovery` 1.0.0，现有登记、文档及接口测试 Skill 不变。通过 `list_platform_skills` / `get_platform_skill` 获取带摘要的包；不会自动安装到个人目录。部署后客户端需重新连接以刷新 MCP 工具清单，再按用户选择安装该 Skill。
