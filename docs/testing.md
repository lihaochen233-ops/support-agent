# 开发与测试

## 开发环境

需要 Go 1.25+、Node.js 22.12+、PostgreSQL / pgvector 和 Redis。依赖版本由 `go.sum` 和 `frontend/package-lock.json` 记录。

生成 `.env` 后，可以单独启动 Compose 的基础设施：

```sh
docker compose -f compose.yaml -f compose.test.yaml up -d postgres redis
```

将 `.env` 中 `DATABASE_URL` 的宿主机端口改为 55432、`REDIS_URL` 改为 56379，保留对应密码。开发终端分别执行：

```sh
go run ./cmd/server
```

```sh
cd frontend
npm ci
npm run dev
```

Vite 代理 `/api` 和 WebSocket 到 8090；访问 Vite 输出的 5173 地址。生产构建由 Go 提供 `frontend/dist`。

## 常规检查

```sh
go test ./...
go vet ./...
npm --prefix frontend ci
npm --prefix frontend run format:check
npm --prefix frontend run build
```

缺少测试数据库时，集成测试明确跳过。不能将这种运行结果解释为集成验收通过。

## 集成测试

使用独立测试数据库；账号需要创建 schema 和扩展权限。测试为每个用例创建 `luma_test_*` schema，并仅清理该 schema。数据库保留的 `vector` 扩展不会自动删除。

POSIX shell：

```sh
export TEST_DATABASE_URL='postgres://user:password@127.0.0.1:55432/test_database?sslmode=disable'
export TEST_REDIS_URL='redis://:password@127.0.0.1:56379/0'
go test ./... -count=1 -v
export TEST_PROCESS_RECOVERY=1
go test ./internal/platform -run '^TestIntegrationAIWorkerProcessRecovery$' -count=1 -v
```

PowerShell 使用 `$env:TEST_DATABASE_URL='...'` 设置同名环境变量。示例连接值必须替换为测试环境的实际配置。

进程恢复测试真正终止请求模型中的 Worker 子进程，等待生产代码的 60 秒租约自然到期，再验证另一 Worker 接管且只保存一份回复。该测试耗时约一分钟，默认不启用；子进程 helper 只由父测试启动。

Go race 检查需要支持 CGO 的编译环境。Linux CI 会执行 `go test -race ./... -count=1`，并启用进程恢复测试。

## 模型测试边界

自动化测试采用本地 HTTP 模型替身，覆盖工具白名单、来源校验、有限重试、向量维度、图片字节传递、无依据转人工与澄清上限；业务数据、租约和向量查询使用真实 PostgreSQL。

真实模型验收需要已开通的 API Key 和业务问题集。上传资料后检查来源是否对应答案，再测试未知政策、模糊问题、问题截图、生成期间转人工和停用引用文档等场景。记录所用模型、端点、文档版本及失败样例，不使用替身结果宣称模型准确率。

## CI

`.github/workflows/ci.yml` 包含 Go 检查与集成测试、前端检查、Compose 配置检查、容器启动和停止一个实例后的网关检查。容器检查采用独立随机配置，关闭 AI，不需要生产密钥。CI 配置存在不代表工作流已经运行通过。
