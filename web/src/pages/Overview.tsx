import { Blocks, CircleAlert, FolderSync, KeyRound, Layers3, LaptopMinimalCheck, RefreshCw, Route, ScanSearch, ShieldCheck, Users } from 'lucide-react'
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
  // 工作流的每个节点都挂当前的真实数字：图是活的，才值得放在概览首屏。
  const steps: Step[] = [
    { id: 'bundles', side: 'cloud', icon: Blocks, label: '内容发布', note: `${published} 项已发布 · 共 ${bundles.data?.bundles.length ?? 0} 项`, loading: bundles.loading, status: published > 0 ? 'ok' : 'idle' },
    { id: 'groups', side: 'cloud', icon: Layers3, label: '权限组装配', note: `${activeGroups} 个在用权限组`, loading: groups.loading, status: activeGroups > 0 ? 'ok' : 'idle' },
    { id: 'assignments', side: 'cloud', icon: Route, label: '授权策略', note: `${activeAssignments} 条有效授权`, loading: assignments.loading, status: activeAssignments > 0 ? 'ok' : 'idle' },
    { id: 'resolve', side: 'cloud', icon: ShieldCheck, label: '服务端解析', note: `${activeMembers} 名活跃成员`, loading: members.loading, status: activeMembers > 0 ? 'ok' : 'idle' },
    { id: 'sync', side: 'local', icon: FolderSync, label: 'ith5 sync', note: `近 24h ${syncedToday} 台设备同步`, loading: audit.loading, status: syncedToday > 0 ? 'ok' : 'idle' },
    { id: 'install', side: 'local', icon: LaptopMinimalCheck, label: '本机落盘', note: `${devices} 台设备已注册`, loading: members.loading, status: staleCount > 0 ? 'warn' : devices > 0 ? 'ok' : 'idle' },
    { id: 'hook', side: 'local', icon: KeyRound, label: 'hook 上报', note: '仅工具调用元数据', status: 'ok' },
    { id: 'audit', side: 'local', icon: ScanSearch, label: '审计事件', note: `${audit.data?.entries.length ?? 0} 条分发记录`, loading: audit.loading, status: (audit.data?.entries.length ?? 0) > 0 ? 'ok' : 'idle' },
  ]
  return <><PageHeader title="组织运行概览" description="配置、权限与终端同步状态，一处掌握。" /><div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"><Metric label="已发布内容" value={published} note={`${bundles.data?.bundles.length ?? 0} 项内容`} icon={Blocks} loading={bundles.loading} error={bundles.err} /><Metric label="活跃成员" value={activeMembers} note={`${groups.data?.groups.filter((g) => !g.archived).length ?? 0} 个权限组`} icon={Users} loading={members.loading || groups.loading} error={members.err || groups.err} /><Metric label="有效授权" value={activeAssignments} note="已排除到期授权" icon={KeyRound} loading={assignments.loading} error={assignments.err} /><Metric label="长期未同步" value={stale.data?.machines.length ?? 0} note="需要管理员关注" icon={CircleAlert} loading={stale.loading} error={stale.err} /></div>
    <div className="mt-4 grid gap-4 xl:grid-cols-[1.45fr_.75fr]"><Card><CardHeader className="flex-row items-center border-b"><CardTitle>ith5 工作流</CardTitle><span className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"><span className="size-1.5 rounded-full bg-success" />实时状态</span></CardHeader><CardContent className="p-5"><WorkflowFlow steps={steps} /><div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground"><span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-accent ring-1 ring-primary/30" />云端管控</span><span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-emerald-50 ring-1 ring-emerald-500/30" />员工本机</span><span className="ml-auto">流动线表示自动链路，虚线表示由人发起</span></div></CardContent></Card>
      <Card><CardHeader className="flex-row items-center border-b"><CardTitle>最近事件</CardTitle><RefreshCw className="ml-auto size-4 text-muted-foreground" /></CardHeader><CardContent className="p-4">{audit.loading ? <div className="space-y-3">{[1,2,3,4].map((i) => <Skeleton key={i} className="h-12" />)}</div> : audit.err ? <Err msg={audit.err} /> : recent.length === 0 ? <p className="py-10 text-center text-sm text-muted-foreground">暂无分发事件</p> : <div>{recent.map((e, i) => <div key={`${e.created_at}-${i}`} className="flex gap-3 border-b py-3 last:border-0"><span className="mt-1.5 size-2 shrink-0 rounded-full bg-success" /><div className="min-w-0"><div className="truncate font-medium">{e.bundle_name} <span className="font-normal text-muted-foreground">v{e.version}</span></div><div className="truncate text-xs text-muted-foreground">{e.email} · {e.hostname || '未知设备'}</div></div><span className="ml-auto shrink-0 text-xs text-muted-foreground">{fmtTime(e.created_at)}</span></div>)}</div>}</CardContent></Card></div>
    <div className="mt-4"><QuickStart /></div>
  </>
}
