# teamai-cli：`server` 仓库类型（零 Git 接入）

这是给 [Tencent/teamai-cli](https://github.com/Tencent/teamai-cli) 的补丁，让它直接以 ith5-server 为团队仓库，
成员机器上不需要 Git。补丁基于 teamai-cli `main`（34e1cfb，v0.23 之后）。

```sh
git clone https://github.com/Tencent/teamai-cli && cd teamai-cli
git am ../ith5-harness-plat/contrib/teamai-cli/0001-server-repo-kind.patch
npm ci && npm run build            # dist/index.js 即带 --server 的 teamai
```

## 成员怎么用

```sh
teamai init --server https://console.example          # 浏览器输入验证码
teamai init --server https://console.example --code ABCD-1234   # 或管理员给的接入码
teamai push            # 本地改动 → 变更集 → 后台审核
teamai contribute --file note.md   # 分享经验，直接发布（含密钥会被拒）
teamai remove rules old-rule       # 删除走同一条审核
```

每次会话启动自动 `pull`；投票、会话摘要、用量在 `pull` 时上报到 `/v1/reports/events`。

## 改了什么

| 文件 | 作用 |
|---|---|
| `src/server-repo.ts` | `/v1` 客户端：设备流登录、凭据（0600）、绑定、快照 + 按哈希取 blob、journal 物化、变更集、上报 |
| `src/server-format.ts` | 快照 → 团队仓布局的渲染器，与服务端 `internal/teamaifmt` 输出一致 |
| `src/server-write.ts` | server 模式的 push / contribute / remove / 上报 |
| `src/init.ts` | `initServer`：登录、选项目、绑定、首次同步、种 AI 工具目录、注入 hook、pull |
| `src/pull.ts` | `refreshTeamRepo` 的 server 分支；自动上报改走 HTTP |
| `src/push.ts` `src/contribute.ts` `src/remove.ts` | server 分支 |
| `src/hook-handlers.ts` | 会话结束的投票同步改为上报 |
| `src/status.ts` | 显示服务端、项目、revision |
| `src/types.ts` | `repo.kind: 'server'`、`repo.bindingId`、`teamai.yaml` 的 `mode: server` |
| `src/index.ts` | `init --server` / `--code`、`push --fast-track` |
| `src/__tests__/server-*.test.ts` | 渲染、journal 冲突、设备流、同步、变更集合并、上报 |

不改任何现有模式（git / http / self）的行为；219 个测试文件全过。

## 服务端对应

`ith5-server` 的接口见 [docs/开发规格.md](../../docs/开发规格.md) §1。`cmd/ith5-materialize` 保留为 Go 版参考实现，
员工不再需要它。
