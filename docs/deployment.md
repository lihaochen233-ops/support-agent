# 部署与运维

## 部署结构

`compose.yaml` 启动 PostgreSQL / pgvector、Redis、数据库初始化任务、两个应用实例和 Nginx。只有网关向宿主机回环地址开放 8090；数据库与 Redis 在容器网络内访问。附件、知识原始文件保存在两个应用共享的 `uploads` 卷。

配置脚本只生成 `.env`，不会修改已有配置。Linux / macOS 使用 `sh scripts/setup.sh`，Windows 使用 `./scripts/setup.ps1`。

## 配置

| 变量 | 用途 |
| --- | --- |
| `POSTGRES_PASSWORD` / `REDIS_PASSWORD` | Compose 基础设施密码，由初始化脚本随机生成 |
| `DATABASE_URL` / `REDIS_URL` | 独立运行 Go 时的连接地址；Compose 覆盖为容器内地址 |
| `APP_ADDR` | Go 监听地址，默认 `:8090` |
| `HTTP_PORT` | Compose 网关映射的宿主机端口，默认 `8090`；改动时同步调整 `APP_ORIGINS` |
| `APP_ORIGINS` | 允许的浏览器来源，逗号分隔，须与访问协议、域名和端口一致 |
| `COOKIE_SECURE` | HTTPS 部署设为 `true` |
| `INSTANCE_ID` | 实例标识，Compose 分别设置为 `app1`、`app2` |
| `UPLOAD_DIR` / `STATIC_DIR` | 附件目录和前端静态资源目录 |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | 首次创建管理员时使用，不重置已有账号 |
| `SEED_DEMO` | 默认 `false`；开启后按 `AGENT_EMAIL` / `AGENT_PASSWORD` 创建示例客服 |
| `CHAT_*` / `EMBEDDING_*` | 模型地址、模型名与密钥 |
| `MODEL_TIMEOUT` | 单次模型请求超时，默认 `45s`，允许 `1s` 到 `2m` |
| `SEARCH_MIN_SCORE` | 检索相似度阈值，默认 `0.25`，需要业务问题集校准 |

脚本生成十六进制密码，可以直接嵌入连接 URL。自定义密码若含保留字符，须正确进行 URL 编码。不要将 `.env`、数据库备份或附件提交到仓库。

## HTTPS 入口

在同一主机的反向代理或既有 TLS 网关上终止 HTTPS，再转发到 `http://127.0.0.1:8090`。入口必须支持 WebSocket Upgrade、至少 11 MB 请求体和不小于 100 秒的代理读取超时；证书申请、续期及 DNS 由部署环境管理。

对外域名确定后，将 `.env` 调整为：

```dotenv
APP_ORIGINS=https://support.your-domain.tld
COOKIE_SECURE=true
SEED_DEMO=false
```

替换上述占位域名，重建应用使配置生效：

```sh
docker compose up -d --force-recreate app1 app2
```

网关通过 Docker 内置 DNS 动态解析应用地址，缓存有效期 10 秒。不要把 Go、PostgreSQL 或 Redis 端口直接暴露到公网。

## 健康与告警

```sh
docker compose ps -a
docker compose logs --tail 100 init app1 app2 gateway
curl --fail http://127.0.0.1:8090/api/health
```

`/api/health` 返回数据库、Redis 和实例状态。数据库不可用时 HTTP 为 503；Redis 不可用时服务仍可通过数据库补拉工作，HTTP 可能仍为 200，监控必须同时检查 `redis` 字段。

建议监控 HTTP 错误、长期等待会话、失败文档、模型错误与耗时、数据库连接、磁盘空间、备份完成及恢复验证。当前 Redis 故障时限流会降级，公网入口应同时配置限流和连接上限。应用日志不能替代外部告警系统。

## 一致性备份

数据库记录引用共享卷中的附件和知识文件，必须一起备份。下面的 POSIX shell 流程暂停应用写入以取得一致备份；适用于本机 Docker 引擎，备份目录不要包含空格。操作安排在维护窗口执行：

```sh
set -eu
umask 077
mkdir -p backups
stamp=$(date -u +%Y%m%dT%H%M%SZ)
docker compose stop gateway app1 app2
trap 'docker compose start app1 app2 gateway' EXIT
docker compose exec -T postgres pg_dump -U luma -d luma -Fc -f /tmp/luma.dump
docker compose cp postgres:/tmp/luma.dump "backups/$stamp.database.dump"
docker run --rm --volumes-from "$(docker compose ps -a -q app1)":ro \
  -v "$PWD/backups:/backup" alpine:3.22 \
  tar -czf "/backup/$stamp.uploads.tar.gz" -C /data .
docker compose exec -T postgres rm -f /tmp/luma.dump
docker compose start app1 app2 gateway
trap - EXIT
```

保留 `.env` 的加密副本，并将数据库与附件备份成对保存到主机之外。恢复必须在隔离环境验证：停止应用，向空数据库执行 `pg_restore --no-owner`，向空共享卷解压对应附件归档并恢复 UID/GID 10001 的访问权限，然后启动匹配版本的应用。`pg_dump` / `pg_restore` 客户端应与服务器版本相匹配，不在未经验证的生产数据库上覆盖恢复。

## 更新与回退

更新前记录代码提交、镜像版本和配置，并执行成对备份。在隔离环境完成迁移、登录、文字图片消息、转人工、接单和知识检索验证后，再进入维护窗口更新：

```sh
docker compose build
docker compose stop gateway app1 app2
docker compose run --rm init
docker compose up -d app1 app2 gateway
```

迁移记录包含校验值，不应修改已应用的 SQL 文件。已有兼容迁移可以回退到旧镜像；存在不兼容迁移时，应恢复匹配的数据库和附件备份，不直接假定旧代码兼容新结构。

## 故障处理

| 现象 | 检查及恢复 |
| --- | --- |
| 登录失败 / WebSocket 被拒 | 检查 `APP_ORIGINS`、HTTPS、Cookie 和 Upgrade 转发 |
| 文档失败 | 查看后台错误；修复密钥、格式或模型配置后重试 |
| AI 不可用 | 检查两套模型配置与区域、调用日志、配额和超时；人工流程可继续 |
| 实例退出 | 重启实例；未完成 AI 任务在租约过期后由其他实例领取 |
| Redis 中断 | 恢复 Redis，检查订阅与在线状态；客户端周期补拉恢复消息 |
| 数据库不可用 | 恢复连接后检查健康状态；不要通过清空持久化卷解决连接问题 |

双应用实例不等于整套系统高可用；数据库、Redis、共享文件和主机仍需按目标可靠性另行设计冗余。
