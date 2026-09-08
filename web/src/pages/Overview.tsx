import { Blocks, CircleAlert, KeyRound, LaptopMinimalCheck, RefreshCw, Route, Users } from 'lucide-react'
import { api } from '@/api'
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
  const nodes: { icon: typeof Blocks; label: string; note: string }[] = [{ icon: Blocks, label: '内容发布', note: `${published} 个版本` }, { icon: Route, label: '权限解析', note: `${activeAssignments} 条授权` }, { icon: LaptopMinimalCheck, label: '终端同步', note: `${devices} 台设备` }]
  return <><PageHeader title="组织运行概览" description="配置、权限与终端同步状态，一处掌握。" /><div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"><Metric label="已发布内容" value={published} note={`${bundles.data?.bundles.length ?? 0} 项内容`} icon={Blocks} loading={bundles.loading} error={bundles.err} /><Metric label="活跃成员" value={activeMembers} note={`${groups.data?.groups.filter((g) => !g.archived).length ?? 0} 个权限组`} icon={Users} loading={members.loading || groups.loading} error={members.err || groups.err} /><Metric label="有效授权" value={activeAssignments} note="已排除到期授权" icon={KeyRound} loading={assignments.loading} error={assignments.err} /><Metric label="长期未同步" value={stale.data?.machines.length ?? 0} note="需要管理员关注" icon={CircleAlert} loading={stale.loading} error={stale.err} /></div>
    <div className="mt-4 grid gap-4 xl:grid-cols-[1.45fr_.75fr]"><Card><CardHeader className="flex-row items-center border-b"><CardTitle>分发链路</CardTitle><span className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"><span className="size-1.5 rounded-full bg-success" />实时状态</span></CardHeader><CardContent className="p-5"><div className="relative grid grid-cols-3 gap-3 text-center before:absolute before:left-[15%] before:right-[15%] before:top-6 before:h-px before:bg-gradient-to-r before:from-primary before:to-emerald-400">{nodes.map(({ icon: Icon, label, note }) => <div className="relative" key={label}><span className="mx-auto mb-2 grid size-12 place-items-center rounded-xl border bg-card text-primary"><Icon className="size-5" /></span><strong className="block text-sm font-medium">{label}</strong><span className="text-xs text-muted-foreground">{note}</span></div>)}</div><div className="mt-6 rounded-lg bg-slate-950 p-4 font-mono text-xs leading-7 text-slate-300"><div><span className="text-primary">$</span> ith5 sync</div><div className="text-emerald-400">✓ <span className="text-slate-300">permissions resolved from current policy</span></div><div className="text-emerald-400">✓ <span className="text-slate-300">{devices} registered devices visible</span></div></div></CardContent></Card>
      <Card><CardHeader className="flex-row items-center border-b"><CardTitle>最近事件</CardTitle><RefreshCw className="ml-auto size-4 text-muted-foreground" /></CardHeader><CardContent className="p-4">{audit.loading ? <div className="space-y-3">{[1,2,3,4].map((i) => <Skeleton key={i} className="h-12" />)}</div> : audit.err ? <Err msg={audit.err} /> : recent.length === 0 ? <p className="py-10 text-center text-sm text-muted-foreground">暂无分发事件</p> : <div>{recent.map((e, i) => <div key={`${e.created_at}-${i}`} className="flex gap-3 border-b py-3 last:border-0"><span className="mt-1.5 size-2 shrink-0 rounded-full bg-success" /><div className="min-w-0"><div className="truncate font-medium">{e.bundle_name} <span className="font-normal text-muted-foreground">v{e.version}</span></div><div className="truncate text-xs text-muted-foreground">{e.email} · {e.hostname || '未知设备'}</div></div><span className="ml-auto shrink-0 text-xs text-muted-foreground">{fmtTime(e.created_at)}</span></div>)}</div>}</CardContent></Card></div>
  </>
}
