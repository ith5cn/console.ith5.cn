import { Blocks, Bot, BrainCircuit, CircleAlert, FileText, FlaskConical, FolderCog, GitBranch, KeyRound, PackageCheck, RefreshCw, Route, ShieldCheck, Users } from 'lucide-react'
import { api } from '@/api'
import { QuickStart } from '@/components/QuickStart'
import { WorkflowFlow, type Step } from '@/components/WorkflowFlow'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Err, fmtTime, PageHeader, useAsync } from '@/ui'

function Metric({ label, value, note, icon: Icon, loading, error }: { label: string; value: number; note: string; icon: typeof Blocks; loading: boolean; error?: string }) {
  return <Card><CardContent className="p-4"><div className="flex items-center text-sm text-muted-foreground">{label}<Icon className="ml-auto size-4" /></div>{loading ? <Skeleton className="mt-3 h-8 w-16" /> : error ? <div className="mt-3 flex items-center gap-2 text-sm text-destructive"><CircleAlert className="size-4" />暂不可用</div> : <div className="mt-2 text-2xl font-semibold tabular-nums">{value}</div>}<p className="mt-1 text-xs text-muted-foreground">{error || note}</p></CardContent></Card>
}

export function Overview() {
  const bundles = useAsync(() => api.listBundles())
  const groups = useAsync(() => api.listGroups())
  const assignments = useAsync(() => api.listAssignments())
  const members = useAsync(() => api.listMembers())
  const stale = useAsync(() => api.staleMachines())
  const audit = useAsync(() => api.audit())
  const published = bundles.data?.bundles.filter((b) => !b.archived && b.latest_version > 0).length ?? 0
  const activeMembers = members.data?.members.filter((m) => m.status === 'active').length ?? 0
  const activeAssignments = assignments.data?.assignments.filter((a) => !a.expired).length ?? 0
  const devices = members.data?.members.reduce((sum, m) => sum + m.machines, 0) ?? 0
  const recent = [...(audit.data?.entries ?? [])].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)).slice(0, 4)
  const activeGroups = groups.data?.groups.filter((g) => !g.archived).length ?? 0
  const staleCount = stale.data?.machines.length ?? 0
  const syncedToday = new Set((audit.data?.entries ?? []).filter((e) => Date.now() - Date.parse(e.created_at) < 864e5).map((e) => `${e.email}/${e.hostname}`)).size
  // 静态流程负责解释研发闭环，节点末尾的数字来自当前组织 API，兼顾叙事与真实状态。
  const steps: Step[] = [
    { id: 'bundles', side: 'cloud', icon: FolderCog, label: '项目初始化', note: `${published} 项能力就绪`, loading: bundles.loading, status: published > 0 ? 'ok' : 'idle' },
    { id: 'groups', side: 'cloud', icon: FileText, label: '需求生成', note: `${activeGroups} 套规范生效`, loading: groups.loading, status: activeGroups > 0 ? 'ok' : 'idle' },
    { id: 'agents', side: 'cloud', icon: Bot, label: '多 Agent 自动开发', note: `${activeAssignments} 条授权策略`, loading: assignments.loading, status: activeAssignments > 0 ? 'ok' : 'idle' },
    { id: 'router', side: 'local', icon: GitBranch, label: '难度路由', note: `${activeMembers} 名活跃成员`, loading: members.loading, status: activeMembers > 0 ? 'ok' : 'idle' },
    { id: 'review', side: 'local', icon: ShieldCheck, label: '代码审查门', note: '双轮审查', status: 'ok' },
    { id: 'qa', side: 'local', icon: FlaskConical, label: '质量评估', note: staleCount > 0 ? `${staleCount} 项需关注` : '风险受控', loading: stale.loading, status: staleCount > 0 ? 'warn' : 'ok' },
    { id: 'memory', side: 'local', icon: BrainCircuit, label: '记忆闭环', note: `近 24h ${syncedToday} 次同步`, loading: audit.loading, status: syncedToday > 0 ? 'ok' : 'idle' },
    { id: 'delivery', side: 'local', icon: PackageCheck, label: '交付成果', note: `${devices} 台设备已连接`, loading: members.loading, status: devices > 0 ? 'ok' : 'idle' },
  ]
  return <><PageHeader title="组织运行概览" description="配置、权限与终端同步状态，一处掌握。" /><div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"><Metric label="已发布内容" value={published} note={`${bundles.data?.bundles.length ?? 0} 项内容`} icon={Blocks} loading={bundles.loading} error={bundles.err} /><Metric label="活跃成员" value={activeMembers} note={`${groups.data?.groups.filter((g) => !g.archived).length ?? 0} 个权限组`} icon={Users} loading={members.loading || groups.loading} error={members.err || groups.err} /><Metric label="有效授权" value={activeAssignments} note="已排除到期授权" icon={KeyRound} loading={assignments.loading} error={assignments.err} /><Metric label="长期未同步" value={stale.data?.machines.length ?? 0} note="需要管理员关注" icon={CircleAlert} loading={stale.loading} error={stale.err} /></div>
    <Card className="mt-4 overflow-hidden border-slate-800 bg-[#080d1b] text-slate-100 shadow-[0_24px_70px_rgba(15,23,42,.18)]"><CardHeader className="relative z-20 flex-row items-center border-b border-white/10 px-6 py-4"><div><CardTitle className="flex items-center gap-2"><Route className="size-4 text-violet-300" />ith5 · AI 自动开发工作流</CardTitle><p className="mt-1 text-xs text-slate-500">从初始化到交付，串联需求拆解、多 Agent 并行开发、双轮审查与动态 QA</p></div><span className="ml-auto hidden items-center gap-2 rounded-full border border-emerald-300/15 bg-emerald-300/[.06] px-3 py-1.5 font-mono text-[10px] uppercase tracking-[.14em] text-emerald-300 sm:flex"><span className="size-1.5 animate-pulse rounded-full bg-emerald-300 shadow-[0_0_10px_rgba(110,231,183,.8)]" />Live topology</span></CardHeader><CardContent className="relative p-0"><div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_50%_0%,rgba(124,92,255,.12),transparent_40%),radial-gradient(circle_at_0%_55%,rgba(34,211,238,.07),transparent_28%)]" /><WorkflowFlow steps={steps} /><div className="relative z-20 flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-white/10 px-6 py-3 font-mono text-[10px] text-slate-500"><span className="flex items-center gap-2"><span className="h-px w-6 bg-cyan-300" />自动执行链路</span><span className="flex items-center gap-2"><span className="h-px w-6 border-t border-dashed border-rose-300/70" />质量门禁回路</span><span className="ml-auto">节点状态连接组织实时数据</span></div></CardContent></Card>
    <div className="mt-4 grid gap-4 xl:grid-cols-[.75fr_1.45fr]"><Card><CardHeader className="flex-row items-center border-b"><CardTitle>最近事件</CardTitle><RefreshCw className="ml-auto size-4 text-muted-foreground" /></CardHeader><CardContent className="p-4">{audit.loading ? <div className="space-y-3">{[1,2,3,4].map((i) => <Skeleton key={i} className="h-12" />)}</div> : audit.err ? <Err msg={audit.err} /> : recent.length === 0 ? <p className="py-10 text-center text-sm text-muted-foreground">暂无分发事件</p> : <div>{recent.map((e, i) => <div key={`${e.created_at}-${i}`} className="flex gap-3 border-b py-3 last:border-0"><span className="mt-1.5 size-2 shrink-0 rounded-full bg-success" /><div className="min-w-0"><div className="truncate font-medium">{e.bundle_name} <span className="font-normal text-muted-foreground">v{e.version}</span></div><div className="truncate text-xs text-muted-foreground">{e.email} · {e.hostname || '未知设备'}</div></div><span className="ml-auto shrink-0 text-xs text-muted-foreground">{fmtTime(e.created_at)}</span></div>)}</div>}</CardContent></Card><QuickStart /></div>
  </>
}
