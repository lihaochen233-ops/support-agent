# 验证记录

记录日期：2026-10-04。所有数据来自本次实际执行；不把测试模型的回答当作云模型验收结果。

## 环境

- Windows 本机，Go 1.26.5，Vue / TypeScript / Vite 生产构建。
- 本地可移植 PostgreSQL 18.6、pgvector 0.8.6、Redis 8.10.2；均只监听回环地址。数据库测试逐个创建隔离 schema。
- 部署文件目标为 PostgreSQL 17 / pgvector 0.8.2、Redis 7.4、两个 Go 实例和 Nginx。该容器组合尚未在本机运行，不能用本地版本测试代替容器验收。
- 云端 API Key 未配置；真实人工链路使用真实数据库与 Redis，模型协议与故障分支使用自动化测试内的 HTTP 替身。

## 自动化结果

`go test ./... -count=1`：31 个顶层测试通过，包含 11 个真实中间件集成测试；平台包约 3.78 秒。耗时的进程恢复测试和仅供子进程使用的 helper 默认明确跳过；`cmd/server` 无测试文件。

另外设置 `TEST_PROCESS_RECOVERY=1`，单独运行 `TestIntegrationAIWorkerProcessRecovery`：通过，用时 60.62 秒。测试在模型请求期间真正 `Process.Kill` 首个 Worker，等待生产代码的 60 秒租约自然到期，再由第二个 Worker 接管。原任务最终 completed，generation=2、attempts=2，数据库仅保存 1 份 AI 回复和原有 1 条任务。没有修改租约时间；两个模型 HTTP 请求均由本地测试替身处理。合计已验证 32 个顶层测试（12 个集成测试）。

`go vet ./...`：通过。`npm --prefix frontend run build`：TypeScript 检查和 Vite 生产构建通过。

| 场景 | 验证方式及结果 |
| --- | --- |
| 并发接单 | 两名客服并发争抢，只有一个成功；未分配客服不能回复 |
| 消息去重与排序 | 并发消息、相同 client_id 重试，检查唯一记录、连续序号与分页 |
| 权限与附件 | 访客隔离、未分配客服限制、附件归属、伪造身份、CSRF、图片格式和像素边界 |
| 跨实例通信与恢复 | 两个独立 Server / HTTP 端口共享真实 PG 和 Redis；A 保存后 B 的 WebSocket 收到通知；关闭 A 后 B 仍可用原身份补拉 |
| Redis 丢通知 | 保存一条不广播的消息，客户端按序号查询仍可恢复 |
| AI 租约与唯一提交 | 租约过期重新领取、代次检查、旧执行者提交被拒、后续消息续任务 |
| 实际进程中断 | 真正终止执行模型请求的 OS 子进程，自然等待 60 秒租约，接管后仅保存 1 份回复 |
| 转人工竞态 | 任务已开始后转人工，迟到结果不能保存 |
| RAG 与图像协议 | 真实向量表 + HTTP 测试模型，检索、来源校验、授权图片字节、多轮工具链路通过 |
| 引用变更 | 生成期间停用文档，旧来源不能随答案提交 |
| 文档替换 | 新版成功后切换；处理失败保留原版；模型指纹不匹配不混查 |
| 不可回答 | 无知识转人工，最多一次澄清，未开放工具被拒绝 |
| 模型错误 | 临时失败有限重试，认证错误不重复重试，异常向量拒绝 |
| PDF / 文档解析 | 可提取文字 PDF 保留页码，无效 PDF / 非 UTF-8 / 空文件明确失败 |
| AI 解决归因 | 必须有 AI 回复、访客明确 solved 且未转人工；空会话、未反馈、转人工后的解决均不计入 AI 解决 |

测试源码位于 `internal/platform/*_test.go`。本机常规测试 JSON 输出在 `.local/test-results.jsonl`（不纳入版本控制）。CI 配置启用了耗时进程恢复测试，还包含 `go test -race`、容器配置检查和镜像构建；CI 尚未实际运行，Windows 本机未运行 race 检查。

初始化脚本在独立测试目录分别使用 Windows PowerShell 5.1 和 PowerShell 7 验证：首次生成四个不同的 36 位随机十六进制密码，重复执行不改变配置文件 SHA；三个脚本均通过 PowerShell 5.1 语法检查。没有覆盖实际 `.env`。Nginx 已依据 1.28 支持的动态 DNS 能力配置 resolver、共享 zone 和 resolve，容器实际运行仍待验证。

## 浏览器实测

使用本地 `http://localhost:8090`，没有注入演示消息或统计数值：

1. 访客免注册发出“耳机无法配对，想请人工客服帮忙。”，状态转为等待人工，显示暂无人工在线可继续留言。
2. 演示客服登录、切换接待中、选择等待会话并接单，完整聊天和交接摘要可见。
3. 客服发送回复，访客刷新后保留身份及完整记录；消息状态为“已保存”。
4. 客服结束接待，访客可提交“已解决”反馈，历史变为只读。
5. 管理后台显示咨询 1、转人工 1、访客解决 1、AI 解决 0，符合真实归因。后续再次进入咨询页会创建新活动会话，因此当前累计数可能高于截图。
6. 上传示例 `service-policy.md`，缺少向量密钥时显示“向量模型 API Key 未配置，配置后可重试”以及重试按钮。
7. 访客页面在 390 × 844 视口下检查，页面宽度 390，无横向溢出；桌面工作台与管理后台完成截图检查。未发现浏览器控制台 error。

实测中修复了接单切换列表丢失选中会话、补拉游标被 ACK 提前推进、管理员出现不可执行的客服编辑入口等问题。

截图：[首页](screenshots/home.png)、[客服工作台](screenshots/desk.png)、[管理统计](screenshots/admin.png)、[手机咨询](screenshots/mobile.png)。

## 尚需外部条件的验收

以下内容没有宣称通过：

- **真实云模型问答与图片理解质量**：需要在 `.env` 填入聊天和向量密钥，并确认模型和端点属于已开通区域。测试替身仅验证程序协议和控制逻辑。
- **Docker Compose 完整启动、Nginx 路由、容器 kill 恢复**：本机没有可用 Docker 引擎。已有双 Server 中间件测试、租约过期测试和真实 OS 进程 Kill 恢复测试，但不能替代实际容器停止演练。
- **长时间压力、慢网及真实手机键盘兼容性**：没有给出未经测量的并发量或可用性承诺。

云模型验收建议使用两个示例知识文档，依次提问产品价格、售后政策、文档外政策、模糊问题和问题截图；核对来源片段。随后在模型生成期间点击转人工、停用来源文档，检查旧答案不能进入会话。云端调用会产生实际费用。

## 部署后的故障演练

在拥有 Docker 的环境中运行：

```powershell
docker compose up --build -d
docker compose ps -a
docker compose logs --tail 100 init app1 app2 gateway
docker compose stop app1
# 保持访客与客服窗口打开，继续留言、刷新并检查历史。
docker compose start app1
```

开启真实 AI 后，可在调用记录与服务日志中确认执行实例，再停止该实例；等待租约到期后应由存活实例完成任务，数据库最终只能保存一份回复。再停止 Redis 验证周期补拉可恢复消息；恢复 Redis 后通知应自动重连。

向量接口参数可对照[阿里云官方文档](https://www.alibabacloud.com/help/en/model-studio/embedding)。该文档说明 `text-embedding-v4` 支持 `dimensions` 参数；具体模型可用性和区域地址仍以账号控制台为准。

聊天工具参数可对照[阿里云 Function Calling 文档](https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen-function-calling)，其中列出 Qwen3-VL-Plus 系列支持工具调用。
