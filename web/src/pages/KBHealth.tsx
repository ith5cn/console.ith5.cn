import { api, type Learning } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { navigate } from '@/navigation'
import { Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

function LearningList({ title, hint, items, empty }: { title: string; hint: string; items: Learning[]; empty: string }) {
  return (
    <Card><CardContent className="p-5">
      <h2 className="font-semibold">{title}</h2>
      <p className="mb-3 text-xs text-muted-foreground">{hint}</p>
      {items.length === 0 ? <p className="text-sm text-muted-foreground">{empty}</p> : (
        <ul className="divide-y text-sm">
          {items.map((l) => (
            <li key={l.id} className="flex cursor-pointer items-center gap-3 py-2 hover:text-primary" onClick={() => navigate('knowledge', l.id)}>
              <span className="min-w-0 flex-1 truncate">{l.title}</span>
              <span className="whitespace-nowrap text-xs text-muted-foreground">召回 {l.recalled} · 赞 {l.upvoted}</span>
              <Badge variant={l.confidence >= 0.7 ? 'success' : l.confidence < 0.15 ? 'destructive' : 'default'}>{Math.round(l.confidence * 100)}%</Badge>
            </li>
          ))}
        </ul>
      )}
    </CardContent></Card>
  )
}

function Trend({ points }: { points: { day: string; count: number }[] }) {
  const max = Math.max(1, ...points.map((p) => p.count))
  return (
    <div className="flex h-24 items-end gap-1">
      {points.map((p) => <div key={p.day} title={`${p.day}: ${p.count}`} className="flex-1 rounded-t bg-primary/70" style={{ height: `${Math.max(4, (p.count / max) * 100)}%` }} />)}
    </div>
  )
}

export function KBHealth() {
  const { data, err, loading } = useAsync(() => api.kbHealth())
  if (loading) return <TableSkeleton />
  if (err || !data) return <Err msg={err || '暂无数据'} />
  const h = data
  return (
    <>
      <PageHeader title="知识库健康" description="最近 30 天的召回趋势，以及应当归档或晋升的经验。置信度低于阈值的进入清理候选，高于阈值的进入晋升候选，阈值在组织策略里调。" />
      <div className="grid gap-4 lg:grid-cols-3">
        <Card><CardContent className="p-5"><h2 className="mb-3 font-semibold">按类型</h2><ul className="text-sm">{Object.entries(h.by_kind).map(([k, n]) => <li key={k} className="flex justify-between py-1"><span>{k}</span><span className="tabular-nums text-muted-foreground">{n}</span></li>)}</ul></CardContent></Card>
        <Card><CardContent className="p-5"><h2 className="mb-3 font-semibold">按作者</h2><ul className="text-sm">{Object.entries(h.by_author).sort((a, b) => b[1] - a[1]).slice(0, 8).map(([k, n]) => <li key={k} className="flex justify-between py-1"><span className="truncate">{k}</span><span className="tabular-nums text-muted-foreground">{n}</span></li>)}</ul></CardContent></Card>
        <Card><CardContent className="p-5"><h2 className="mb-3 font-semibold">召回趋势</h2><Trend points={h.recall_trend} /><div className="mt-1 flex justify-between text-xs text-muted-foreground"><span>{h.recall_trend[0]?.day ?? ''}</span><span>{h.recall_trend[h.recall_trend.length - 1]?.day ?? ''}</span></div></CardContent></Card>
      </div>
      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <LearningList title="最常被召回" hint="被客户端注入上下文最多的经验。" items={h.top_recalled} empty="还没有召回记录。" />
        <LearningList title="沉默的经验" hint="发布超过两周但从未被召回，可能标题不够具体。" items={h.silent} empty="所有经验都有被用到。" />
        <LearningList title="建议归档" hint="置信度低于清理阈值。" items={h.prune_candidates} empty="没有需要清理的。" />
        <LearningList title="建议晋升" hint="置信度高于晋升阈值，适合沉淀为正式 rule / doc。" items={h.promote_candidates} empty="暂时没有达到晋升标准的。" />
      </div>
      <p className="mt-4 text-xs text-muted-foreground">统计截至 {fmtTime(new Date().toISOString())}</p>
    </>
  )
}
