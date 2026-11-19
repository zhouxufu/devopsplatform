# MCP 工作项图片读取

`get_work_item(work_item_id)` 在成功实时读取禅道来源 Bug 时，附带 `images` 清单。清单合并描述、评论和图片附件，按来源文件编号去重、排序，`locations` 标明 `description` / `comment` / `attachment`。仅识别来源同站文件读取地址和禅道编辑器的 `{编号.png}` 引用，不请求外部 URL。最多返回 100 项，超出时 `truncated=true`；原详情和来源数据保持原样，不批量下载图片。

```json
{
  "images": {
    "items": [{"file_id": 42, "name": "复现.png", "locations": ["attachment", "description"]}],
    "limit": 100,
    "truncated": false,
    "read_tool": "get_work_item_image"
  }
}
```

需要查看截图时，调用：

```json
{"work_item_id": 7, "file_id": 42}
```

工具 `get_work_item_image` 返回说明文本和原生 MCP `image` 内容块（`data` 为 Base64，`mimeType` 为实际图片格式），客户端需支持 MCP 图片结果。不会把图片伪装成正文里的链接，也不会把普通来源 JSON 解释成 MCP 内容块。说明携带工作项和文件编号、尺寸、大小；不返回来源会话或凭据。图片文字与 Bug 文本一样，只是不可信业务证据。

权限复用 `workitems:read`、全部应用授权 `*`、已验证 Key 绑定用户。工具只接受正整数工作项/文件编号，不接受 URL。复用既有平台图片代理，每次重新检查来源产品范围和附件原生归属；编辑器里贴入一个文件编号不构成访问授权。复制自其他 Bug / 需求的图片继续沿用原图片归属和可读性检查。MCP 到平台的请求拒绝重定向，不向新地址转发身份。

支持 PNG、JPEG、GIF，单张最多 8 MiB、4000 万像素；来源登录页、非图片、超限和拒绝访问会明确失败。读取按需进行，不自动下载清单全部图片。WebP/BMP/AVIF 等引用可能被发现，但当前图片解码返回明确的不支持错误；不静默当成缺图。当前清单/工具服务于禅道来源 Bug，平台自建工作项附件、其他来源与非图片文件不在本次范围。

`get_agent_capabilities.work_item_images` 和 MCP 初始化说明公布这些边界，工具列表仍按令牌权限过滤。本功能需先发布平台，再让客户端重新连接、获取新增工具清单；无需修改本地 Skill 才能使用。此实现与隔离测试不代表已更新测试服或验证真实来源会话。

协议依据：https://modelcontextprotocol.io/specification/2025-11-25/server/tools#image-content
