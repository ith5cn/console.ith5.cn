# ITH5

**企业 AI 编码配置中台。** 开发者继续用自己本机的 Claude Code，公司在云端统一管理
skill / command / agent 的内容、版本和权限，并留下「谁拿了什么、谁跑了什么」的审计记录。

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](./go.mod)
![Status](https://img.shields.io/badge/status-内部试点中%20(v0.x)-orange.svg)

> **定位是治理与效率，不是安全管控。** 客户端控制不是安全边界——这是官方
> 文档自己写的。做不到什么见下面「明确做不到的」。

<sub>**In English** — ITH5 is a self-hosted control plane for Claude Code configuration in
companies. Admins publish skills / commands / agents into permission groups on the server;
developers run `ith5 sync` on their own machines and get exactly what they're entitled to,
with versioning, instant rollback and usage auditing. Server is a single self-contained Go
binary (migrations and admin UI embedded); the only dependency is PostgreSQL.</sub>

---

## 快速上手（自建服务端）

只要有 Docker，两条命令就能跑起来看看：

```sh
make db-up          # 起本地 Postgres（映射到 55432，避开本机 5432）
make web-build      # 构建管理后台（产物编译进 ith5-server）
make build          # 构建三个二进制

export ITH5_DATABASE_URL='postgres://ith5:ith5@localhost:55432/ith5?sslmode=disable'
export ITH5_JWT_SECRET='至少32字节的密钥'

./bin/ith5-server -seed     # 写入演示数据
./bin/ith5-server           # 启动（含管理后台，迁移自动执行）
```

打开 <http://localhost:8080>，用 `demo@example.com` / `demo-password` 登录（组织 `demo`）。

部署到 AWS ECS 见 [deploy/ecs/README.md](./deploy/ecs/README.md)。

## 快速上手（员工侧）

```sh
curl -fsSL https://<服务端地址>/install.sh | sh
ITH5_SERVER=https://<服务端地址> ith5 login
ith5 sync
```

装好后打开 `claude`，公司分发的技能就在 `/` 菜单里。实测全程 **6 秒**。

| 命令 | 作用 |
|---|---|
| `ith5 login` | 登录并绑定本机 |
| `ith5 sync` | 同步公司分发的内容 |
| `ith5 status` | 查看当前状态 |
| `ith5 doctor` | 逐项体检，给出可执行的建议 |
| `ith5 logout` | 登出（`--purge` 同时移除已安装内容） |

---

## 它做什么

| 能力 | 说明 |
|---|---|
| 按权限组分发 | 权限组装配 skill/command/agent，人授权到组。往组里加内容，所有被授权者下次同步自动拿到 |
| 版本与回滚 | 回滚 = 用旧内容发布新版本，客户端永不处理降级语义；内容已在本地时**零下载** |
| 不碰用户文件 | 只操作一个入口（skill 是 `skills/<name>`，agent 是 `agents/<name>.md`），从不进入用户目录内部增删 |
| 执行审计 | 谁、何时、用什么工具、动了哪个仓库的哪个文件。**不含**文件内容、diff、完整命令、提示词 |
| 撤权回收 | 停用账号后客户端自动清理本机内容。**切断续期，不保证擦除**——见下 |

### 明确做不到的

- 阻止开发者读取或复制已下发到本机的内容
- 阻止开发者卸载 ith5
- **保证离职者本机的文件被擦除**——移除动作依赖对方主动运行 sync

因此：**敏感能力必须放在服务端 API 之后**，本地那份只是空壳。这是发布准入检查项，
不是建议。

---

## 架构

```
Go   = 跑在员工机器上的（ith5 CLI + ith5-hook）与服务端（ith5-server）
TS   = 管理后台（Vite + React，编译进服务端二进制）
DB   = PostgreSQL（唯一外部依赖；迁移编译进二进制，启动时自动执行）
```

落盘模型：内容写进不可变的 `~/.ith5/store/<bundle>/<内容摘要>/`，
`~/.claude/skills/<name>` 只是指向它的**软链**（Windows 用目录联接，
降级时复制）。store 是真相，目标是一次性的——中断后不修复，重做即可，
因此不需要事务日志。

**两种落盘形态**：skill/command 落成目录 `skills/<name>/`；agent 落成单文件
`agents/<name>.md`。后者更简单——覆盖普通文件的 `rename` 本就是原子的，因此
没有空窗、不需要回收站。归属判定则相反：单文件没地方放标记，改用内容比对，
**员工手工改过的 agent 会被认作用户自有，永不覆盖**。见技术方案 §8.2.0。

| 平台 | 落盘方式 | 需要设置 |
|---|---|---|
| macOS / Linux / WSL | 符号链接 | 无 |
| Windows (NTFS) | 目录联接 | **无**（需提权的是 `mklink /D`，我们用 `/J`） |
| 网络盘 / FAT32 | 复制 + 双 rename | 无 |

---

## 服务端配置

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `ITH5_DATABASE_URL` | ✅ | — | PostgreSQL DSN |
| `ITH5_JWT_SECRET` | ✅ | — | ≥32 字节。**生产环境只从 secret manager 注入** |
| `ITH5_LISTEN_ADDR` | | `:8080` | 监听地址 |
| `ITH5_BASE_URL` | | `http://localhost:8080` | 会写进 `install.sh` |
| `ITH5_DIST_DIR` | | `dist` | 客户端二进制目录 |

启动开关：`-migrate` 只跑迁移后退出；`-seed` 写入演示数据后退出（**勿在生产使用**）。

首个管理员用 `ITH5_INIT_ORG_SLUG` / `ITH5_INIT_ORG_NAME` / `ITH5_INIT_EMAIL` /
`ITH5_INIT_NAME` / `ITH5_INIT_PASSWORD` 配合 `-init-owner` 初始化，详见 [docs/管理员手册.md](./docs/管理员手册.md)。

---

## 开发

```sh
make test           # 全部测试
make perf           # hook 延迟回归（p95 < 50ms 是硬指标）
make fmt            # gofmt + go vet
make web-dev        # 前端开发模式，API 代理到 localhost:8080
bash scripts/build-release.sh v0.1.0   # 交叉编译各平台 + 校验清单
```

### 代码结构

```
cmd/ith5              客户端 CLI
cmd/ith5-hook         hook 入口（独立二进制，p95 2.8ms）
cmd/ith5-server       服务端（自带迁移与管理后台）
internal/core         纯逻辑，零依赖：权限解析、checksum、同步计划、路径安全
internal/cli          store、三种落盘策略、sync 编排、队列上传
internal/hook         事件白名单提取、git 仓库识别、队列
internal/api          HTTP 层
internal/db           仓储层（手写 pgx）
web/                  管理后台
db/migrations/        SQL 迁移（goose 格式，embed 进服务端二进制）
```

`internal/core` **保持零运行时依赖**：它承载「错了会出安全问题」的逻辑，
必须能被完整审计。canonical JSON 那 30 行是自己写的，不引第三方——
checksum 是安全相关的，实现要能一眼看完。

### 改代码前

**落盘（`internal/cli/link`、`internal/cli/store.go`）和权限解析
（`internal/core/permissions.go`）这两处要格外小心。** 它们的语义定错了会一路错
下去，且都有测试钉住——测试失败通常意味着你改变了一个刻意的设计，而不是修了个
bug。动手前先读该文件顶部的注释，那里写明了为什么是现在这个样子。

---

## 文档

**使用**

- [docs/员工上手.md](./docs/员工上手.md) — 三条命令、常见问题、记录了什么（发给同事的就是这份）
- [docs/管理员手册.md](./docs/管理员手册.md) — 发布、权限组、授权、审计、排障、部署
- [docs/试点检查清单.md](./docs/试点检查清单.md) — 内部试点该盯什么信号，以及每个信号对应改什么
- [deploy/ecs/README.md](./deploy/ecs/README.md) — AWS ECS 部署步骤

**关于代码注释里的 `§` 引用**

代码和 SQL 注释里大量出现 `技术方案 §8.2.0`、`PRD §9.3` 这样的引用，指向内部的
产品需求文档与技术方案。**这两份文档不在开源范围内**，但每处引用旁边都写清了该
条约束本身是什么——注释可以独立阅读，不必去找原文。

---

## 项目状态

**v0.x，内部试点阶段。** 接口和数据结构可能出现不兼容变更，升级前请看 release notes。
生产使用前请自行完成安全评审——特别是上面「明确做不到的」那一节。

## 参与贡献

欢迎 issue 和 PR。提 PR 前请确保 `make test` 和 `make fmt` 通过，并在描述里
说明改动触及了技术方案的哪一节。

**安全问题请勿提公开 issue**，用 GitHub Security Advisory 私下报告。

## License

[MIT](./LICENSE)
