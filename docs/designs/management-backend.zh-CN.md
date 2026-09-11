# TeamAI 管理后端（Go）设计

> 对应 Tencent/teamai-cli#341。英文版见 [management-backend.md](./management-backend.md)，
> 两份的章节、接口名、状态机、阶段与验收标准保持一致。分主题的详细设计在 [`docs/`](../) 根目录，
> 接口总表在 [`docs/开发规格.md`](../开发规格.md)。

## 1. 能力映射与领域模型

Git 团队仓的每一项读写能力都有后端对应：

| Git 团队仓 | 后端 |
|---|---|
| clone / pull | `GET /v1/bindings/{id}/snapshot` + `GET /v1/blobs/{sha256}`（全量清单、按哈希取内容、ETag / 304） |
| commit | 带 `put` / `delete` 操作的变更集 |
| branch | `draft` 状态的变更集 |
| MR / PR | `in_review` 状态的变更集与审核记录 |
| merge | `POST /v1/change-sets/{id}/publish`（一个事务，可跨项目） |
| Git revision | 每个层级、每个绑定的 `revision = sha256(规范化快照)` |
| Git conflict | 发布时基线过期且与后续发布重叠 → `412 REVISION_MISMATCH` |
| revert | 回滚 = 重新发布历史内容的新变更集 |
| `teamai.yaml`、`skills/`、`rules/`、`docs/`、`env/`、`agents/`、`hooks/`、`mcp/`、`learnings/`、`culture.md`、`claudemd/` | 资源 kind：skill、rule、doc、env、agent、hook、mcp、learning、culture、claudemd、policy |
| `tags.yaml`、`manifest/roles.yaml`、`manifest/projects.yaml` | 由资源标签、权限组授权与绑定的项目生成 |
| `members/`、`stats/`、`votes/`、`sessions/` | 上报面：`POST /v1/reports/events`（usage_daily、skill_usage、vote_delta、session_summary、tool_use） |
| `sources` | 不支持（非目标） |

领域对象：Organization、Team、Project、WorkspaceBinding；Account、User（成员记录）、Role、Device；
Resource、ResourceVersion、Revision；ChangeSet、Review、Release；Learning、Vote、UsageEvent、Session、AuditEvent。
两个平面：**已发布资源面**（资源、版本、发布、快照）与**用户上报面**（投票、会话、用量、设备状态）。

规则：越具体的层级优先（project > team > org > 权限组）；下层 tombstone 屏蔽上层；删除是显式操作并通过下一次快照传播；
版本是资源内不可变的单调整数；发布校验基线，重叠的过期改动被拒；放进权限组的资源只经授权分发。

## 2. 零 Git 接入用户旅程

- **J1 SSO 首登**：后台 → `GET /v1/auth/oidc/authorize?org=<slug>` → IdP → 回调 → 建账号、按分组映射建成员关系 → 会话令牌。
- **J2 非 Git 用户加入单个项目**：管理员生成限定项目的接入码 → 成员 `ith5-materialize login --server … --enroll <码>` →
  浏览器 `/activate` 批准设备流 → 自动建绑定 → `sync` → `teamai init --self` + `teamai pull`。
- **J3 多项目**：`bind --project a,b`（或含多个项目的接入码）；快照同时解析两者，同层冲突标记并在客户端保留原样；
  `projects` 列出可绑定的项目；`unbind` 离开一个目录。
- **J4 跨项目审核发布**：成员改 `.teamai/` 后 `push`；服务端创建（或更新）跨项目的变更集；审核人在后台批准；发布是一个事务；
  下一次 `sync` 拿到新 revision 的 200（未变则 304）。

同时覆盖：离线重试（回执下次补发；304 让重复同步零成本）、权限失败（不可见范围 404，禁止动作 403）、设备解绑与卸载
（`unbind`、`logout --purge`）、账号停用（空清单 → 客户端卸载）。

## 3. 多项目管理模型

组织 → 团队 → 项目，加上留在客户端的个人化配置（排除、标签订阅、本机 env 覆盖）。后台支持项目创建 / 归档、成员角色、
草稿 / 审核 / 发布状态、版本历史、差异预览、回滚、权限组与可到期授权、设备与同步状态、审计。

## 4. 内部鉴权扩展点

`identity.Provider` 接口（`local`、通用 `oidc`；私有协议实现同一接口接入）。外部身份稳定映射为 `accounts(issuer, subject)`；
组织 / 团队 / 项目角色由 `idp_group_mappings` 按优先级从 IdP 分组推导；首次登录自动建档；撤销在一个事务内传播
（refresh family、设备、绑定）；对映射改动后未再登录的成员提供对账报告与应用；IdP 故障时拒绝新登录与写操作，
已签发的 access token 继续可读 15 分钟。服务账号与 SCIM 本期不做。

## 5. API 契约与状态机

版本化前缀 `/v1`；JWT bearer（login / session / device 三种）；`ETag` + `If-Match`（变更集与绑定必带，其余可选）；
写请求 `Idempotency-Key`（24 小时重放，不符 409）；所有列表游标分页；稳定错误码；`X-Client-Version` 对照
`/v1/capabilities` 的 `min_client_version`；blob 以 sha256 校验完整性。

变更集状态：`draft → in_review → approved → published`；`rejected`、`cancelled` 为终态；任何编辑作废审核并回到 `draft`。
审核决定携带提交时的 digest。

## 6. Go monorepo 模块蓝图

```
cmd/ith5-server, cmd/ith5-materialize
internal/{identity, organizations, projects, resources, changesets, releases, sync, knowledge,
          telemetry, audit, maintenance, idempotency, teamaifmt, db, api, platform/{config,httpx,pg,ratelimit,metrics}}
db/migrations, web/
```

基础设施按模块藏在接口后面（各 store、经 resources store 访问 blob、`pg.WithTx` 事务、身份提供者、审计 sink）。
单二进制，只依赖 PostgreSQL。

## 7. 安全与运维要求

所有查询按 `org_id` 隔离租户；项目级 RBAC 由角色推导；三种令牌各有 scope；env 与 secret 分离（`secret: true` 的变量服务端无值）；
所有 kind 的文本文件逐个做密钥扫描；blob 以 sha256 校验；不做远程命令执行；管理动作全部审计；限流；幂等；
保留期策略与每日清理任务；`/metrics`（Prometheus 文本）与 `/healthz`；备份恢复见 `docs/运维-备份与恢复.md`。

## 8. 阶段化实施顺序

S0 清场与身份 → S1 组织与设备 → S2 资源与同步 → S3 变更集与发布 → S4 知识与上报 → S5 后台 → S6 文档与幂等 →
S7 客户端推送、保留期、对账、分页 / 乐观锁 / 客户端版本、密钥、分区布局、指标。全部阶段已完成，验收证据记录在开发规格里。

## 9. 验收标准、开放决策与非目标

验收：`pull.ts`、`push.ts`、`local-agent.ts`、`team-push.ts` 与 `src/resources/` 的每条读写路径都有后端映射（见上表）；
四条旅程用原版 CLI 端到端跑通；鉴权边界覆盖 OIDC、可插拔 provider、分组同步、设备流、撤销与 IdP 故障；本文中英文一致。

留给维护者的决策：物化步骤是否做进 teamai-cli 成为 HTTP 适配器；扁平与分区布局哪个作默认；贡献的 `server/` 目录重新授权。

非目标：`sources`、`packages`、增量同步游标、签名清单、部分批准、服务端三方合并、跨组织原子发布、SCIM、服务账号。
