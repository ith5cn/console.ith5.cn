import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { api, can, getMe, type Policy as PolicyT } from '@/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Err, PageHeader, TableSkeleton, useAsync } from '@/ui'

export function Policy() {
  const orgId = getMe()!.membership.org_id
  const { data, err, loading, reload } = useAsync(() => api.policy(orgId), [orgId])
  if (loading) return <TableSkeleton />
  if (err || !data) return <Err msg={err || '无法加载策略'} />
  return (
    <>
      <PageHeader title="组织策略" description="影响审核流程与知识库治理的组织级开关。改动即时生效，只作用于之后提交的变更集。" />
      <Form initial={data} orgId={orgId} onSaved={reload} />
      {can('organization:manage') && <Maintenance />}
    </>
  )
}

// Maintenance 让 owner 立刻跑一轮保留期清理，不必等每日定时任务。
function Maintenance() {
  const [busy, setBusy] = useState(false)
  const [last, setLast] = useState<Record<string, number> | null>(null)
  async function run() {
    setBusy(true)
    try {
      const r = await api.runMaintenance()
      setLast(r.deleted)
      toast.success(r.errors?.length ? `完成，但有 ${r.errors.length} 个错误` : '清理完成')
    } catch (e) { toast.error((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <Card className="mt-4 max-w-3xl"><CardContent className="p-5">
      <h2 className="font-semibold">保留期清理</h2>
      <p className="mb-3 text-xs text-muted-foreground">服务端每天自动按上面的保留月数删除会话摘要、用量与执行记录，并清理过期的去重账本与令牌。这里可以立刻跑一轮。</p>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="outline" disabled={busy} onClick={run}>{busy ? '清理中…' : '立即清理'}</Button>
        {last && <span className="text-xs text-muted-foreground">{Object.entries(last).filter(([, n]) => n > 0).map(([k, n]) => `${k} ${n}`).join(' · ') || '没有需要删除的数据'}</span>}
      </div>
    </CardContent></Card>
  )
}

function Num({ id, label, hint, value, onChange, step, min, max }: { id: string; label: string; hint: string; value: number; onChange: (n: number) => void; step?: number; min?: number; max?: number }) {
  return (
    <div className="grid gap-1 sm:grid-cols-[1fr_8rem] sm:items-center">
      <div><Label htmlFor={id}>{label}</Label><p className="text-xs text-muted-foreground">{hint}</p></div>
      <Input id={id} type="number" step={step} min={min} max={max} value={value} onChange={(e) => onChange(Number(e.target.value))} />
    </div>
  )
}

function Form({ initial, orgId, onSaved }: { initial: PolicyT; orgId: string; onSaved: () => void }) {
  const [p, setP] = useState(initial)
  const [err, setErr] = useState('')
  useEffect(() => setP(initial), [initial])
  async function save() {
    setErr('')
    try {
      await api.putPolicy(orgId, p)
      toast.success('已保存')
      onSaved()
    } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Card className="max-w-3xl"><CardContent className="grid gap-5 p-5">
      <Num id="pol-approvals" label="需要的批准数" hint="变更集进入 approved 需要多少位审核人批准。admin 的快速通道不受影响。" value={p.required_approvals} min={1} max={5} onChange={(n) => setP({ ...p, required_approvals: n })} />
      <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={p.learnings_review} onChange={(e) => setP({ ...p, learnings_review: e.target.checked })} /><span><span className="font-medium">经验分享需要审核</span><span className="block text-xs text-muted-foreground">默认关闭，成员分享的经验直接发布。打开后走与其他资源相同的审核流程。</span></span></label>
      <Num id="pol-prune" label="清理阈值" hint="置信度低于此值的经验列入建议归档。" value={p.confidence_prune} step={0.05} min={0} max={1} onChange={(n) => setP({ ...p, confidence_prune: n })} />
      <Num id="pol-promote" label="晋升阈值" hint="置信度高于此值的经验列入建议晋升。" value={p.confidence_promote} step={0.05} min={0} max={1} onChange={(n) => setP({ ...p, confidence_promote: n })} />
      <Num id="pol-retention" label="上报数据保留月数" hint="会话摘要、用量与执行记录超过这个月数后清理。" value={p.retention_months} min={1} max={60} onChange={(n) => setP({ ...p, retention_months: n })} />
      <Err msg={err} />
      <div><Button onClick={save}>保存策略</Button></div>
    </CardContent></Card>
  )
}
