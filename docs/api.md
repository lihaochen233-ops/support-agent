# HTTP / WebSocket 接口

请求前缀 `/api`。JSON 使用 snake_case，时间为 RFC3339。列表返回 `{items:[...]}`；失败返回 `{error:"中文提示"}`。业务状态冲突为 409，权限不足为 403，身份失效为 401。

访客请求设置 `X-Surface: visitor`，员工默认 `staff`。所有修改请求需 `X-Requested-With: support-agent`；浏览器 Origin 须匹配 `APP_ORIGINS`。图片和 WebSocket 使用 `?surface=visitor|staff` 选择 Cookie，不传递令牌。

| 接口组 | 路由 |
| --- | --- |
| 公共信息 | GET `/bootstrap`、GET `/health` |
| 身份 | POST `/visitor`、POST `/login`、POST `/logout`、GET `/me`、POST `/presence` |
| 会话 | GET/POST `/conversations`、GET `/conversations/{id}` |
| 消息 | GET/POST `/conversations/{id}/messages` |
| 状态 | POST `/conversations/{id}/handoff|claim|release|assign|close` |
| 反馈与已读 | POST `/conversations/{id}/feedback`、POST `/conversations/{id}/read` |
| 附件 | POST `/attachments`、GET `/attachments/{id}` |
| 人员与统计 | GET/POST `/staff`、PATCH `/staff/{id}`、GET `/stats`、GET `/ai-calls` |
| 知识 | GET/POST `/documents`、GET/DELETE `/documents/{id}`、POST `/documents/{id}/retry|toggle`、POST `/knowledge/search` |

## 发送与补拉

```json
{"client_id":"客户端生成的 UUID","body":"耳机如何配对？","image_id":"可选的已上传附件 UUID"}
```

返回持久化 Message。重试必须复用 client_id，不能每次生成新编号。无分页参数时返回最近消息升序；`before_seq` 加载更早消息，`after_seq` 补拉后续消息；响应含 `has_more`。

WebSocket `/api/ws?surface=visitor`：

```json
{"type":"send","conversation_id":"UUID","client_id":"UUID","body":"你好"}
{"type":"ping"}
```

服务端事件：`ready`、`pong`、`changed`（会话编号）、`ack`（client_id 和完整 message）、`error`（client_id 和错误）。`changed` 只提示刷新，以数据库补拉为准。HTTP 是相同业务逻辑的备用发送通道。

## 上传

附件采用 multipart：`file` 与 `conversation_id`。文档采用 multipart：`file`，替换时附加 `replace_id`。文档启停体 `{enabled:true}`；手动重试空对象。搜索体 `{query:"问题"}`。文档内容与调用记录仅管理员可访问。

## 权限

访客只能操作自己的会话与图片。客服可看到公共等待队列摘要，接单后才能查看完整记录并回复。管理员可查看所有记录、改派会话、管理知识与员工；管理员发送人工消息仍需成为当前接待人。

完整字段约定见 [CONTRACT.md](CONTRACT.md)，实际数据库字段见迁移，服务端实现是最终依据。
