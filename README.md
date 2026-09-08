<div align="center">

# ITH5

### 企业级 AI 编码配置中台

开发者继续用自己本机的 Claude Code，公司在云端统一管理
skill / command / agent 的内容、版本和权限，并记录「谁拿到了什么内容、在哪个仓库调用了什么工具」——
审计的是**工具调用**，不涉及和 AI 的对话内容。

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](./go.mod)
[![Hook p95](https://img.shields.io/badge/hook%20p95-2.8ms-brightgreen.svg)](#开发)
[![Dependencies](https://img.shields.io/badge/外部依赖-仅%20PostgreSQL-informational.svg)](#架构)
![Status](https://img.shields.io/badge/status-内部试点中%20(v0.x)-orange.svg)

**[在线体验 →](https://console.ith5.cn)**　·　[快速上手](#快速上手)　·　[架构](#架构)　·　[文档](#文档)

</div>

<sub>**In English** — ITH5 is a self-hosted control plane for Claude Code configuration in
companies. Admins publish skills / commands / agents into permission groups on the server;
developers run `ith5 sync` on their own machines and get exactly what they're entitled to,
with versioning, instant rollback and usage auditing. Server is a single self-contained Go
binary (migrations and admin UI embedded); the only dependency is PostgreSQL.</sub>

> **定位是治理与效率，不是安全管控。** 客户端控制不是安全边界——这是官方文档自己写的。
> 做不到什么见 [「明确做不到的」](#明确做不到的)。

## 在线体验

不装环境也能先看一眼管理后台长什么样：

- **地址**：<https://console.ith5.cn>
- **组织**：`demo`
- **账号**：`test@ith5.cn`
- **密码**：`xLzhQrXMPs6JTEcgignI0p0j`

> 这是公开演示环境，数据会被重置，请勿放入真实敏感信息。想在自己的机器上跑，见下面的
> [快速上手](#快速上手)。

## 亮点

- **秒级触达**：员工侧从 `curl | sh` 到技能出现在 `/` 菜单，实测全程 **6 秒**
- **hook 几乎零感知**：审计钩子 p95 延迟 **2.8ms**，不拖慢任何一次工具调用
- **单二进制部署**：`ith5-server` 自带迁移、自带管理后台，唯一外部依赖是 **PostgreSQL**
- **零下载回滚**：回滚只是发布一个指向旧内容的新版本，内容已在本地时**零下载**
- **安全相关代码零运行时依赖**：`internal/core` 不引三方包，checksum 实现能一眼看完，方便审计
- **不碰用户文件**：只操作一个软链入口，从不在用户目录里做隐式增删

## 目录

- [在线体验](#在线体验)
- [亮点](#亮点)
- [没有 ITH5 之前](#没有-ith5-之前)
- [它做什么](#它做什么)
- [明确做不到的](#明确做不到的)
- [快速上手](#快速上手)
- [本机工作流看板](#本机工作流看板)
- [架构](#架构)
- [服务端配置](#服务端配置)
- [开发](#开发)
- [文档](#文档)
- [项目状态](#项目状态)
- [参与贡献](#参与贡献)

## 没有 ITH5 之前

公司想让全员用上统一的内部 skill / command / agent，现实往往是这样的：

- 内容在 Slack 里甩一个压缩包，员工手动解压丢进 `~/.claude`，装没装、装的是不是最新版全靠自觉
- 谁改过内容、谁在什么仓库跑了什么，出了事没人说得清
- 员工离职，本机那份内容还在，权限系统里删了账号也收不回文件

ITH5 把这三件事变成服务端一处配置：分发靠权限组自动下发，操作留审计，账号一停用客户端自动清理。

## 它做什么

| 能力 | 说明 |
|---|---|
| 按权限组分发 | 权限组装配 skill/command/agent，人授权到组。往组里加内容，所有被授权者下次同步自动拿到 |
| 版本与回滚 | 回滚 = 用旧内容发布新版本，客户端永不处理降级语义；内容已在本地时**零下载** |
| 不碰用户文件 | 只操作一个入口（skill 是 `skills/<name>`，agent 是 `agents/<name>.md`），从不进入用户目录内部增删 |
| 执行审计 | 谁、何时、用什么工具、动了哪个仓库的哪个文件。**不含**文件内容、diff、完整命令、提示词 |
| 撤权回收 | 停用账号后客户端自动清理本机内容。**切断续期，不保证擦除**——见下 |

## 明确做不到的

- 阻止开发者读取或复制已下发到本机的内容
- 阻止开发者卸载 ith5
- **保证离职者本机的文件被擦除**——移除动作依赖对方主动运行 sync

因此：**敏感能力必须放在服务端 API 之后**，本地那份只是空壳。这是发布准入检查项，不是建议。

## 快速上手

### 自建服务端

只要有 Docker，两条命令就能跑起来看看：

```sh
make db-up          # 起本地 Postgres（映射到 55432，避开本机 5432）
make web-build       # 构建管理后台（产物编译进 ith5-server）
make build           # 构建三个二进制

export ITH5_DATABASE_URL='postgres://ith5:ith5@localhost:55432/ith5?sslmode=disable'
export ITH5_JWT_SECRET='至少32字节的密钥'

./bin/ith5-server -seed     # 写入演示数据
./bin/ith5-server            # 启动（含管理后台，迁移自动执行）
```

打开 <http://localhost:8080>，用 `demo@example.com` / `demo-password` 登录（组织 `demo`）。

### 员工侧

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
| `ith5 watch` | 本机工作流看板（见下） |
| `ith5 logout` | 登出（`--purge` 同时移除已安装内容） |

## 本机工作流看板

用 `/ith5-ai` 这类编排型 skill 跑自动开发时，它的 N1-N8 节点是在主会话里内联执行的，
Claude Code 不提供任何进度界面——你只能翻滚动条猜它走到哪了。`ith5 watch` 把这件事
变成一个页面。

```sh
cd /path/to/你的项目     # 看板从这里读 specs/*/tasks.md
ith5 watch
```

它会开启本机 trace、补齐所需 hook、起一个**只监听 `127.0.0.1`** 的看板并打开浏览器。
参数：`--port` 固定端口、`--project` 指定项目根、`--no-open` 不开浏览器。

> **装完必须重启 Claude Code。** settings.json 只在会话启动时读取，已经开着的会话
> 不会加载新 hook——此时看板会一直空白，且**不会有任何报错**提示你。

看板上有三块：**正在运行**（当前在跑哪些 agent / skill，秒数实时走，并行派发会同时列出）、
**Task 进度**（读 `specs/<feature>/tasks.md` 的复选框，`3/6 · 50%`）、**最近调用**（已完成项与耗时）。

Task 进度刻意取自磁盘而非事件流：`[x]` 标记本就是 ith5-ai 断点续跑依赖的那份真相，
事件会漏，磁盘上的复选框不会。Edit/Bash/Read 一律不显示——一次会话几百条，
只会把「工作流走到哪了」淹掉。

### 它记什么、不记什么

trace 是**独立于审计队列的第二条流**，写在 `~/.ith5/trace/<session>.ndjson`，
**只落本机、永不上传**。两者的开关（`trace.enabled` / `telemetry.enabled`）互不影响：
`ith5 watch` 不会开启上报。

之所以另开一条而不复用审计队列：看板需要 subagent 名、skill 名、task 描述，
这些一旦上传就越过了审计的 L0 禁运清单；而审计队列在未登录时完全静默，
看板却是开发者看自己本机的工具，不该要求登录。

`ith5 watch` 装的 hook（合并写入，保留你已有的 handler，备份为 `settings.json.ith5.bak`）：

| 事件 | matcher | 用途 |
|---|---|---|
| `PreToolUse` | `Task\|Agent\|Skill` | agent/skill 的**开始**点——「正在运行」全靠它 |
| `PostToolUse` | `Edit\|Write\|MultiEdit\|Bash\|Task\|Agent\|Skill` | 后半段记结束与耗时；前半段属审计，看板不用 |
| `SessionStart` | — | 标记会话开始 |
| `SubagentStop` | — | 兜底：agent 非正常结束时 PostToolUse 可能不来 |

关掉：把 `~/.ith5/config.json` 的 `trace.enabled` 改为 `false` 即停止记录，
`~/.ith5/trace/` 可随时删除；连 hook 一并卸掉用 `ith5 logout --purge`。

## 架构

```
Go   跑在员工机器上的（ith5 CLI + ith5-hook）与服务端（ith5-server）
TS   管理后台（Vite + React，编译进服务端二进制）
DB   PostgreSQL（唯一外部依赖；迁移编译进二进制，启动时自动执行）
```

**落盘模型**：内容写进不可变的 `~/.ith5/store/<bundle>/<内容摘要>/`，
`~/.claude/skills/<name>` 只是指向它的**软链**（Windows 用目录联接，降级时复制）。
store 是真相，目标是一次性的——中断后不修复，重做即可，因此不需要事务日志。

**两种落盘形态**：skill/command 落成目录 `skills/<name>/`；agent 落成单文件
`agents/<name>.md`。后者更简单——覆盖普通文件的 `rename` 本就是原子的，因此没有
空窗、不需要回收站。归属判定则相反：单文件没地方放标记，改用内容比对，**员工手工
改过的 agent 会被认作用户自有，永不覆盖**。见技术方案 §8.2.0。

| 平台 | 落盘方式 | 需要设置 |
|---|---|---|
| macOS / Linux / WSL | 符号链接 | 无 |
| Windows (NTFS) | 目录联接 | **无**（需提权的是 `mklink /D`，我们用 `/J`） |
| 网络盘 / FAT32 | 复制 + 双 rename | 无 |

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
`ITH5_INIT_NAME` / `ITH5_INIT_PASSWORD` 配合 `-init-owner` 初始化，详见
[docs/管理员手册.md](./docs/管理员手册.md)。

## 开发

```sh
make test           # 全部测试
make perf            # hook 延迟回归（p95 < 50ms 是硬指标）
make fmt             # gofmt + go vet
make web-dev         # 前端开发模式，API 代理到 localhost:8080
bash scripts/build-release.sh v0.1.0   # 交叉编译各平台 + 校验清单
```

### 代码结构

```
cmd/ith5              客户端 CLI
cmd/ith5-hook         hook 入口（独立二进制，p95 2.8ms）
cmd/ith5-server       服务端（自带迁移与管理后台）
internal/core         纯逻辑，零依赖：权限解析、checksum、同步计划、路径安全
internal/cli          store、三种落盘策略、sync 编排、队列上传
internal/hook         事件白名单提取、git 仓库识别、队列；本机 trace 流
internal/watch        本机看板：trace 跟踪、状态还原、SSE 推送、内嵌页面
internal/api          HTTP 层
internal/db           仓储层（手写 pgx）
web/                  管理后台
db/migrations/        SQL 迁移（goose 格式，embed 进服务端二进制）
```

`internal/core` **保持零运行时依赖**：它承载「错了会出安全问题」的逻辑，必须能被
完整审计。canonical JSON 那 30 行是自己写的，不引第三方——checksum 是安全相关的，
实现要能一眼看完。

### 改代码前

**落盘（`internal/cli/link`、`internal/cli/store.go`）和权限解析
（`internal/core/permissions.go`）这两处要格外小心。** 它们的语义定错了会一路错下去，
且都有测试钉住——测试失败通常意味着你改变了一个刻意的设计，而不是修了个 bug。动手前
先读该文件顶部的注释，那里写明了为什么是现在这个样子。

## 文档

- [docs/员工上手.md](./docs/员工上手.md) — 三条命令、常见问题、记录了什么（发给同事的就是这份）
- [docs/管理员手册.md](./docs/管理员手册.md) — 发布、权限组、授权、审计、排障、部署
- [docs/试点检查清单.md](./docs/试点检查清单.md) — 内部试点该盯什么信号，以及每个信号对应改什么

代码和 SQL 注释里大量出现 `技术方案 §8.2.0`、`PRD §9.3` 这样的引用，指向内部的产品
需求文档与技术方案。**这两份文档不在开源范围内**，但每处引用旁边都写清了该条约束
本身是什么——注释可以独立阅读，不必去找原文。

## 项目状态

**v0.x，内部试点阶段。** 接口和数据结构可能出现不兼容变更，升级前请看 release notes。
生产使用前请自行完成安全评审——特别是上面[「明确做不到的」](#明确做不到的)一节。

## 参与贡献

欢迎 issue 和 PR。提 PR 前请确保 `make test` 和 `make fmt` 通过，并在描述里说明改动
触及了技术方案的哪一节。

**安全问题请勿提公开 issue**，用 GitHub Security Advisory 私下报告。

## License

[MIT](./LICENSE)
