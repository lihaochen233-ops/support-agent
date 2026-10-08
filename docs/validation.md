# 验证记录

验证日期：2026-10-08。

## 已执行

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 命令通过；本次未设置测试数据库与 Redis，相关集成用例明确跳过 |
| `go vet ./...` | 通过 |
| `npm --prefix frontend ci` | 按 lockfile 安装通过 |
| `npm --prefix frontend run format:check` | 通过 |
| `npm --prefix frontend run build` | TypeScript 检查与 Vite 生产构建通过 |
| PowerShell 初始化 | Windows PowerShell 5.1 隔离目录验证通过；生成随机密码，默认不创建示例客服，重复执行不覆盖配置 |
| Shell 初始化 | Git Bash 的 POSIX shell 与 OpenSSL 验证通过；生成随机密码，默认不创建示例客服，重复执行不覆盖配置 |

自动化模型测试使用本地 HTTP 替身，验证工具协议、错误处理和回答来源约束，不代表云端模型的实际效果。

## 集成测试覆盖

源码包含真实 PostgreSQL / pgvector、Redis 和 OS 子进程测试，覆盖：

- 消息幂等、连续序号、分页、会话和附件权限。
- 两名客服同时接单，只有一个成功。
- 跨实例通知、实例退出后读取历史、丢通知后的数据库补拉。
- AI 租约代次、迟到回复拒绝、转人工竞争与后续任务。
- 实际终止 Worker 子进程后，等待自然租约到期恢复且只保存一份回复。
- 文档版本切换、停用引用、向量模型指纹和失败恢复。
- AI 解决统计要求实际 AI 回复、访客明确反馈且未转人工。

运行方法见 [开发与测试](testing.md)。必须依据实际执行日志确认集成测试通过，不能以默认跳过视为通过。

## 尚未在本次环境执行

- Docker Compose 完整启动、Nginx 网关和容器停止恢复；本次环境没有可用 Docker 引擎。
- 配置于 GitHub Actions 的 race 与容器检查；尚未在 GitHub 上触发。
- 真实云模型问答、截图理解及业务问题集评估；需要可用的云端 API Key。
- 生产环境的性能负载、TLS 入口和备份恢复演练。

上述项目应在目标部署环境验收后更新记录。发布仓库内容与完成生产环境验收是不同的步骤。
