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

## 请求字段

| 接口 | 请求体 / 查询参数 |
| --- | --- |
| POST `/visitor` | `{name?:string}`，复用有效的访客身份 |
| POST `/login` | `{email,password}` |
| POST `/presence` | `{available:boolean}` |
| GET `/conversations` | `scope=mine\|waiting\|history\|all`、`status`、`q`；服务端按身份过滤 |
| POST `/conversations` | `{}`，返回当前未结束会话或新建会话 |
| POST `/{id}/handoff` | `{reason?:string}`，仅用于该访客的会话 |
| POST `/{id}/claim\|release\|close` | `{}` |
| POST `/{id}/assign` | `{agent_id}`，仅管理员 |
| POST `/{id}/feedback` | `{value:"solved"\|"unsolved"}` |
| POST `/{id}/read` | `{seq:number}`，游标不能超过会话最新序号 |
| POST `/staff` | `{name,email,password,role:"agent"}` |
| PATCH `/staff/{id}` | `{name?,enabled?,password?}`，管理客服账号 |
| GET `/ai-calls` | `limit`，返回最近的模型调用记录 |

上表中 `/{id}` 的完整前缀为 `/conversations/{id}`。消息正文最多 4000 个 Unicode 字符，正文和图片至少提供一个。

会话状态为 `ai`、`waiting`、`human`、`closed`。消息包含 `id`、`conversation_id`、`seq`、`sender_role`、`sender_id`、`client_id`、`body`、`image_id`、`citations`、`created_at`。引用包含实际片段编号、文档名称、正文及页码或段落。

## 统计与健康

`GET /stats` 返回 `conversations`、`waiting`、`human`、`closed`、`handoffs`、`ai_solved`、`solved`、`unsolved`、`avg_wait_seconds`、`avg_first_response_seconds`、`model_calls`、`total_tokens`。

`handoffs` 按转接记录计数，退回队列后重新接待可产生新的周期。等待与首响均按对应转接周期计算。`ai_solved` 必须存在 AI 回复、访客明确反馈已解决且未转人工；未转人工本身不代表解决。

`GET /health` 返回 `{ok,db,redis,instance_id}`。数据库失败时 HTTP 503；Redis 故障会单独报告，详见部署文档中的监控要求。
