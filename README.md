<div align="center">

# ITH5

### teamai-cli 的 Go 管理后端

员工继续用 [teamai-cli](https://github.com/Tencent/teamai-cli) 拉取团队的 skill / rule / agent / MCP / hook / 经验；
公司在服务端统一管理组织、团队、项目、审核、发布、权限与审计。不需要 Git，不需要员工理解仓库。

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](./go.mod)
[![Dependencies](https://img.shields.io/badge/外部依赖-仅%20PostgreSQL-informational.svg)](#架构)
![Status](https://img.shields.io/badge/status-对齐%20teamai--cli%20%23341-orange.svg)

</div>

<sub>**In English** — ITH5 is a self-hosted Go management backend for teamai-cli, built against
[Tencent/teamai-cli#341](https://github.com/Tencent/teamai-cli/issues/341). It replaces the Git team
repo with a server: organizations, teams, projects, device login without Git, changesets with review
and atomic publish, hash-addressed sync with revisions, learnings with secret scanning, weekly digest,
and audit. Single Go binary with embedded migrations and admin console; the only dependency is
PostgreSQL. English design docs live in [docs/en/](./docs/en/).</sub>

> 这个仓库的目标是成为 teamai-cli issue #341 所描述的「Go 管理后端」的实现，
> 最终以 `server/` 的形式贡献到 teamai-cli。在此之前它独立发布，接口以 [docs/开发规格.md](./docs/开发规格.md) 为准。

## 架构一图

![ith5-server 架构](docs/images/architecture-server.svg)

员工机器上的 teamai-cli 走 `/v1` 拉快照、提交变更集、上报数据；管理员在嵌入的后台审核发布；企业身份源通过 OIDC 接入。
服务端是分层单体，每个领域一个包，数据全在 PostgreSQL 里。

![teamai-cli 内部与 server 模式](docs/images/architecture-teamai-cli.svg)

teamai-cli 的四种团队仓来源都只负责把"当前应得的资源"放进同一个目录，后面的资源处理链与各 AI 工具的适配完全共用。
`server` 模式是 [contrib/teamai-cli/](./contrib/teamai-cli/) 里的补丁新增的：不需要 Git，读写与上报全走后台。

## 它解决什么

teamai-cli 用一个 Git 仓库承载团队资源：clone、branch、push、MR。对开发者很自然，对不熟悉 Git 的人是门槛；
多项目之间的成员、资源、权限、版本也没有统一入口。ITH5 把这些搬到服务端：

| Git 团队仓里的动作 | ITH5 里的对应 |
|---|---|
| clone 仓库 | 设备授权流登录，或管理员发一个接入码 |
| 切换项目命名空间 | 一台设备的一个目录绑定若干项目 |
| `teamai push` 开 MR | 变更集：草稿 → 审核 → 发布，一次发布跨多项目原子生效 |
| MR 合并 | 发布事务，生成新 revision |
| `teamai pull` | 全量快照 + 按文件哈希取内容，未变化返回 304 |
| `teamai contribute` | learning 直接发布，服务端做密钥扫描 |
| `teamai digest` / KB Health | 周报、知识库健康，数据来自客户端上报 |
| 仓库权限 | 三层角色推导权限；权限组做可选内容包；停用账号立即撤销设备 |

## 快速上手

### 1. 起服务端

```sh
make db-up          # 本地 Postgres，映射到 55432
make web-build      # 构建管理后台，产物编译进二进制
make build          # bin/ith5-server 与 bin/ith5-materialize

export ITH5_DATABASE_URL='postgres://ith5:ith5@localhost:55432/ith5?sslmode=disable'
export ITH5_JWT_SECRET='至少32字节的随机密钥'

./bin/ith5-server -seed    # 演示数据：组织 demo，demo@example.com / demo-password
./bin/ith5-server          # 启动；迁移自动执行
```

打开 <http://localhost:8080> 登录管理后台。

### 2. 员工侧

员工机器上只需要 Node 20 和带 `server` 模式的 teamai-cli（补丁在 [contrib/teamai-cli/](./contrib/teamai-cli/)，
已提给上游）。不需要 Git，目录不需要是仓库，没有仓库地址和凭据要填。

```sh
npm install -g teamai-cli

cd ~/work/billing
teamai init --server https://console.example          # 终端显示验证码，在浏览器里输入即完成登录
                                                      # 或：--code ABCD-1234 用管理员发的接入码，连项目都不用选
```

`init` 会登录、让你选项目（只有一个时自动选）、绑定当前目录、同步并装进 `.claude/`、`settings.json`、`.mcp.json`。
之后每次打开 AI 工具自动同步。改了本地的 skill / rule 想推回去：

```sh
teamai push                          # 改动打成变更集并提交审核，终端给出后台链接
teamai contribute --file note.md     # 分享一条经验，直接发布；含密钥会被拒
teamai remove rules old-rule         # 删除走同样的审核
```

投票、会话摘要、用量在同步时自动上报，周报和知识库健康度由此而来。管理员停用账号后，下一次同步拿到空清单，本机内容被卸载。

`cmd/ith5-materialize` 是同一套协议的 Go 参考实现，留给写其他适配器的人。

详细步骤见 [docs/员工上手.md](./docs/员工上手.md)，管理员操作见 [docs/管理员手册.md](./docs/管理员手册.md)。

## 能力一览

| 领域 | 内容 |
|---|---|
| 身份 | 账号与组织成员分离；密码登录、设备授权流、通用 OIDC；refresh 轮换与重用检测；接入码；撤销一个事务内传播 |
| 组织 | 组织 → 团队 → 项目；三层角色（owner / admin / member / viewer）推导十一种权限；审核人名单；组织策略 |
| 资源 | 11 种 kind：skill、rule、doc、agent、hook、mcp、env、claudemd、culture、policy、learning；org / team / project 三个层级，最具体者优先，tombstone 屏蔽，policy 字段级取严 |
| 变更集 | draft → in_review → approved → published；digest 变了审核作废；`required_approvals` 组织级配置；admin 快速通道；发布事务锁 content_heads，基线过期但不重叠自动放行 |
| 同步 | 全量快照，revision = 清单哈希，ETag / 304；blob 按 sha256 直接下载并校验可见性；sync-results 回执写分发日志 |
| 客户端 | teamai-cli `server` 模式（[contrib/teamai-cli/](./contrib/teamai-cli/)）：`init --server` 零 Git 接入，pull / push / contribute / remove / 上报全走 `/v1` |
| 知识 | learning 直接发布；密钥命中 422 `SECRET_DETECTED`；置信度 = 点赞 / 召回 × 时间衰减；归档、晋升为 rule / doc / skill；PostgreSQL 全文检索 |
| 上报 | 五类事件统一入口，按 (device, event_id) 去重；服务端白名单解析，提示词、输出片段、绝对路径、完整命令行没有落地字段 |
| 审计 | 管理动作、分发记录、执行记录；只存元数据 |
| 协议 | 写请求 `Idempotency-Key`；`ETag` / `If-Match` 乐观锁；所有列表游标分页；`X-Client-Version` 与最低客户端版本协商 |
| 密钥 | env 变量可标 `secret: true`，值只在本机；所有 kind 的文本文件发布前逐个扫描 |
| 运维 | 每日保留期清理与 IdP 对账统计；`/metrics` Prometheus 指标；`/healthz`；备份恢复文档 |

## 明确不做的

- 阻止开发者读取或复制已下发到本机的内容，或保证离职者本机文件被擦除：撤销只切断续期，清理依赖客户端下次同步
- 服务端三方合并、部分批准、跨组织原子发布
- SCIM、企业私有身份协议（留接口）、服务账号
- teamai 的 `sources`、`packages`、增量同步游标、签名清单

因此**敏感能力必须放在服务端 API 之后**，本地那份只是空壳。

## 架构

```
Go   ith5-server（API + 嵌入的管理后台 + 迁移）、ith5-materialize（员工机器上的物化工具）
TS   web/（Vite + React，编译进服务端二进制）
DB   PostgreSQL（唯一外部依赖）
```

```
cmd/ith5-server        服务端入口，装配依赖
cmd/ith5-materialize   login / bind / sync，把快照落成 .teamai/
internal/platform      config、httpx（错误码、JSON）、ratelimit、pg
internal/identity      账号、令牌、设备流、接入码、撤销、OIDC
internal/organizations 组织、团队、成员，Subject.Can 权限推导
internal/projects      项目与绑定
internal/resources     kind、校验、路径安全、层级解析、policy 合并
internal/sync          快照、revision、回执
internal/changesets    状态机、digest、审核
internal/releases      发布事务、content_heads、回滚
internal/knowledge     learning、密钥扫描、置信度、检索
internal/telemetry     事件解析与四张统计表、周报、用量
internal/idempotency   幂等记录模型
internal/audit         审计事件
internal/teamaifmt     把快照渲染成 teamai 的仓库布局
internal/db            仓储层（手写 pgx），每个模块一个 store
internal/api           chi 路由与 handler，按模块分文件
db/migrations          goose 迁移，embed 进二进制
docs/                  设计文档（中文）与 docs/en/（英文）
```

## 服务端配置

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `ITH5_DATABASE_URL` | ✅ | — | PostgreSQL DSN |
| `ITH5_JWT_SECRET` | ✅ | — | ≥32 字节，生产环境只从 secret manager 注入 |
| `ITH5_LISTEN_ADDR` | | `:8080` | 监听地址 |
| `ITH5_BASE_URL` | | `http://localhost:8080` | 对外地址，OIDC 回调与设备授权链接用它 |
| `ITH5_METRICS_TOKEN` | | 空 | 非空时 `GET /metrics` 需要 Bearer；为空则只应在内网暴露 |

启动开关：`-migrate` 只跑迁移后退出；`-seed` 写入演示数据后退出（勿在生产使用）；
`-init-owner` 配合 `ITH5_INIT_ORG_SLUG` / `ITH5_INIT_ORG_NAME` / `ITH5_INIT_EMAIL` / `ITH5_INIT_NAME` / `ITH5_INIT_PASSWORD`
创建第一个组织与 owner。

服务端自身不做 TLS，挂在反向代理后面；探活打 `GET /healthz`。容器构建见 [Dockerfile](./Dockerfile)。

## 开发

```sh
make test        # 单元测试，不需要数据库
make test-db     # 含数据库集成测试（自动建 ith5_test 库，串行执行）
make e2e         # 端到端：打补丁的 teamai-cli 在非 git 目录接入、推送、审核、发布、上报（独立 ith5_e2e 库）
make fmt         # gofmt + go vet
make web-dev     # 前端开发模式，API 代理到 localhost:8080
cd web && npx vitest run
```

端到端验证：`-seed` 后起服务，用 `ith5-materialize` 登录、绑定、同步，再用原版 teamai-cli
`init --self` + `pull`。集成测试覆盖了设备流、撤销、快照与 304、变更集全流程、发布冲突、密钥拒绝、上报去重、幂等键。

## 文档

- [docs/开发规格.md](./docs/开发规格.md) — 接口总表、数据库结构、目录、页面清单、分段验收
- [docs/设计-身份与组织模型.md](./docs/设计-身份与组织模型.md) · [资源类型与层级](./docs/设计-资源类型与层级.md) · [同步协议](./docs/设计-同步协议.md) · [变更集与发布](./docs/设计-变更集与发布.md) · [知识与上报面](./docs/设计-知识与上报面.md)
- [docs/designs/management-backend.md](./docs/designs/management-backend.md) · [zh-CN](./docs/designs/management-backend.zh-CN.md) — 按 #341 交付物结构整理的总设计
- [docs/运维-备份与恢复.md](./docs/运维-备份与恢复.md) — 备份、恢复、数据保留、监控
- [docs/en/](./docs/en/) — 上述设计文档的英文版
- [docs/换客户端方案.md](./docs/换客户端方案.md) — 为什么放弃自研客户端、改为对齐 teamai-cli

## 参与贡献

欢迎 issue 和 PR。提 PR 前请确保 `make test-db`、`make fmt` 与 `cd web && npx tsc -b && npx vitest run` 通过，
并在描述里说明改动触及了哪份设计文档的哪一节。

**安全问题请勿提公开 issue**，用 GitHub Security Advisory 私下报告。

## License

[AGPL-3.0](./LICENSE) · Copyright (c) 2026 ITH5 Contributors

贡献到 teamai-cli 仓库的部分将按该仓库的许可证（MIT）重新授权。
