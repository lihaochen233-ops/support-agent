# Luma · Go AI 客服平台

一个可独立部署的 Go 后端简历项目：访客免注册咨询，Agent 检索业务文档、理解截图并回答；遇到无法确认的问题或访客要求人工时，进入公共接单队列。客服可以接收完整对话、图片、资料引用和交接摘要。

**技术栈：** Go 1.25+、Vue 3 / TypeScript、PostgreSQL / pgvector、Redis、WebSocket、Nginx。模块化单体部署两个实例，使用同一数据库和附件卷。

## 启动（推荐 Docker Compose）

需要 Docker / Docker Compose。Windows 可以使用支持 Linux 容器的 Docker Desktop；配置脚本兼容 Windows PowerShell 5.1 和 PowerShell 7。

```powershell
cd E:\STUDY\项目\support-agent
./scripts/setup.ps1
# 查看 .env 中生成的账号密码；填写 CHAT_API_KEY / EMBEDDING_API_KEY。
docker compose up --build -d
docker compose ps -a
```

打开 [访客首页](http://localhost:8090)、[咨询页面](http://localhost:8090/chat)、[工作台登录](http://localhost:8090/login)。

| 身份 | 默认邮箱 | 密码 |
| --- | --- | --- |
| 管理员 | admin@luma.local | `.env` 的 `ADMIN_PASSWORD` |
| 演示客服 | agent@luma.local | `.env` 的 `AGENT_PASSWORD` |

脚本生成随机密码，不覆盖已有 `.env`。演示客服仅在 `SEED_DEMO=true` 时初始化。重启不会重置账号密码；客服改密在管理后台操作。`init` 容器 `Exited (0)` 表示迁移成功。数据库和 Redis 默认不暴露宿主机端口，网关仅监听本机 8090。

管理员和访客使用不同 Cookie，同一浏览器可分别打开咨询页面和工作台。客服登录后点击右上角“暂离”切换为“接待中”，再到“待接单”领取会话。

## 配置真实 AI

在 `.env` 配置两套服务，地址、密钥和模型必须属于支持的同一云区域：

```dotenv
CHAT_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
CHAT_API_KEY=你的聊天模型密钥
CHAT_MODEL=qwen3-vl-plus
EMBEDDING_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
EMBEDDING_API_KEY=你的向量模型密钥
EMBEDDING_MODEL=text-embedding-v4
```

服务采用兼容的 `/chat/completions` 和 `/embeddings` HTTP 接口，聊天模型须支持图片和工具调用；向量固定 1024 维。更换向量模型或端点后需要重新处理知识文档。不要把密钥放入前端、Git 或截图。

1. 以管理员登录，进入管理后台 → 知识库。
2. 上传 `examples/knowledge` 中的演示资料，等待状态变为“可用”。
3. 用“检索效果预览”检查问题能否找到相关片段。
4. 从访客端提问，检查回答与资料来源，再尝试发截图或转人工。

修改模型配置后需要重启应用。Compose 使用 `docker compose up -d --force-recreate app1 app2`；Nginx 通过 Docker 内置 DNS 动态刷新应用地址，DNS 缓存有效期为 10 秒，容器 IP 变化时无需手动重启网关。当前电脑的本地启动方式先执行 `./scripts/stop-local.ps1`，再执行 `./scripts/start-local.ps1`。已有失败文档点击“重试”即可重新处理。修改 `.env` 的初始化账号密码不会修改数据库中已有账号的密码。

没有配置密钥时，访客聊天和人工接待正常运行，AI 会明确说明不可用并转人工；文档处理会显示配置失败原因。应用不会生成假 AI 答案。`SEARCH_MIN_SCORE=0.25` 只是初始召回阈值，需用自己的问答集校准，不能解释为正确率或模型置信度。

## 已实现功能

- 访客：匿名身份恢复、唯一活动会话、文字/图片、发送重试、分页历史、未读游标、主动转人工、关闭与反馈。
- 人工：登录与接待状态、公共队列、并发接单、会话切换、快捷回复、退回队列、历史记录。
- Agent：多轮上下文、最多三次白名单工具调用、视觉输入、来源校验、最多一次澄清、故障转人工、真实调用日志。
- RAG：TXT / MD / 文字型 PDF、分段与重叠、1024 维向量、精确余弦检索、PDF 页码/文字段落引用、版本替换、失败重试、启停与删除。
- 后台：客服创建、编辑、停用和改密，会话查询/改派，真实统计与模型调用记录。
- 可靠性：数据库事务保存与消息序号、客户端编号幂等、WebSocket + Redis 跨节点通知、重连及在线补拉、AI 任务租约/续租/fencing、转人工状态版本检查。

知识文档 ≤10 MB，PDF ≤50 页；不支持扫描 PDF OCR。聊天图片为 JPEG/PNG/WebP，≤5 MB、≤2000 万像素；模型一次最多接收最近 3 张图片。图片仅通过授权接口访问。

## 本地开发

需要 Go 1.25+、Node.js 22.12+、带 pgvector 的 PostgreSQL、Redis。先配置 `.env` 的本地数据库连接。

```powershell
cd frontend
npm ci
npm run build
cd ..
go run ./cmd/server
```

前后端分开调试：一个终端 `go run ./cmd/server`，另一个终端 `cd frontend; npm run dev`。Vite 将 `/api` 和 WebSocket 代理到 8090。

只使用 Compose 的基础设施时：

```powershell
docker compose -f compose.yaml -f compose.test.yaml up -d postgres redis
# 将 DATABASE_URL / REDIS_URL 的宿主机端口改为 55432 / 56379。
```

本次交付还附有当前电脑使用的本地运行脚本 `scripts/start-local.ps1`，使用已经下载到项目 `.local/runtime` 的可移植组件。它不是仓库的通用安装器；在其他电脑上优先使用 Compose。

```powershell
./scripts/start-local.ps1
# 停止本地 Go 服务（保留数据库与 Redis）：
./scripts/stop-local.ps1
```

当前电脑的 PostgreSQL 实际运行目录记录在 `.local/runtime-path`，避免中文路径造成初始化编码问题。若临时运行时被清理，改用 Compose 或自行配置 PostgreSQL / pgvector 与 Redis。启动 Compose 前先停掉占用 8090 的本地 Go 服务。

## 测试

```powershell
go test ./...
go vet ./...
npm --prefix frontend run build
npm --prefix frontend run format:check
# 设置独立测试数据库和 Redis 后，启用真实中间件测试：
$env:TEST_DATABASE_URL='postgres://用户名:密码@127.0.0.1:55432/数据库名?sslmode=disable'
$env:TEST_REDIS_URL='redis://127.0.0.1:56379/0'
go test ./... -count=1 -v
# 额外运行真实 Worker 进程终止 / 60 秒租约恢复测试：
$env:TEST_PROCESS_RECOVERY='1'
go test ./internal/platform -run '^TestIntegrationAIWorkerProcessRecovery$' -count=1 -v
```

集成测试每次创建独立 `luma_test_*` schema，完成后只删除自己的 schema。需要测试数据库账号有创建 schema 和扩展的权限。没有 `TEST_DATABASE_URL` 时集成测试明确跳过；云端 HTTP 使用测试替身验证协议和故障，不代表真实模型回答质量。详见 [验证记录](docs/validation.md)。

## 目录和阅读顺序

```text
cmd/server/          启动、配置加载、优雅退出
internal/platform/  认证、会话、通信、Agent、知识库和测试
frontend/           Vue 访客端、客服台、管理后台
deploy/             Nginx 双实例入口
examples/knowledge/ 虚构的演示业务资料
docs/               架构、接口、学习路线和验证记录
```

先读 [学习路线](docs/learning.md)，再结合 [架构与失败恢复](docs/architecture.md)、[接口约定](docs/api.md) 跟踪调用链。

## 范围

同一主机的两个应用实例用于验证跨进程协作，不代表数据库、Redis、主机均实现了高可用。不含群聊、音视频、多租户、订单查询、自动退款、OCR、跨主机文件存储。对公网部署时，需要配置 HTTPS、实际 `APP_ORIGINS` 和 `COOKIE_SECURE=true`。
