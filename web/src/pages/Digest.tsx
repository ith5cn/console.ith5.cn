import { useState } from 'react'
import { api, type DigestTotals } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

// 周报只做「本周 vs 上周」的对比；数据来源是客户端上报的会话摘要与 skill 使用计数。
function fmtHours(ms: number) { return `${(ms / 3_600_000).toFixed(1)} h` }
function fmtCost(micros: number) { return `$${(micros / 1_000_000).toFixed(2)}` }

function Stat({ label, value, prev, format }: { label: string; value: number; prev?: number; format?: (n: number) => string }) {
  const f = format ?? ((n: number) => n.toLocaleString('zh-CN'))
  const delta = prev === undefined || prev === 0 ? null : Math.round(((value - prev) / prev) * 100)
  return (
    <Card><CardContent className="p-4">
      <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{f(value)}</div>
      {delta !== null && <div className={`mt-1 text-xs tabular-nums ${delta >= 0 ? 'text-success' : 'text-destructive'}`}>{delta >= 0 ? '+' : ''}{delta}% 较上周</div>}
    </CardContent></Card>
  )
}

function shiftWeek(weekStart: string, days: number) {
  const d = new Date(weekStart)
  d.setDate(d.getDate() + days)
  return d.toISOString().slice(0, 10)
}

export function Digest() {
  const [week, setWeek] = useState<string | undefined>(undefined)
  const { data, err, loading } = useAsync(() => api.digest(week), [week])
  if (loading) return <TableSkeleton />
  if (err || !data) return <Err msg={err || '暂无数据'} />
  const d = data
  const prev: DigestTotals | undefined = d.previous
  const success = d.sessions ? Math.round((d.sessions_succeeded / d.sessions) * 100) : 0
  return (
    <>
      <PageHeader title="周报" description={`${d.week_start} 起的一周，活跃成员 ${d.active_members} 人。数据来自客户端上报的会话摘要，不含任何提示词或代码内容。`}
        action={<div className="flex gap-2"><Button variant="outline" onClick={() => setWeek(shiftWeek(d.week_start, -7))}>上一周</Button><Button variant="outline" onClick={() => setWeek(undefined)}>本周</Button></div>} />
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <Stat label="会话数" value={d.sessions} prev={prev?.sessions} />
        <Stat label="成功率" value={success} format={(n) => `${n}%`} />
        <Stat label="对话轮次" value={d.prompt_turns} prev={prev?.prompt_turns} />
        <Stat label="活跃时长" value={d.active_ms} prev={prev?.active_ms} format={fmtHours} />
        <Stat label="费用" value={d.cost_micros} prev={prev?.cost_micros} format={fmtCost} />
        <Stat label="人工纠正" value={d.corrections} prev={prev?.corrections} />
      </div>
      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card><CardContent className="p-5">
          <h2 className="mb-3 font-semibold">最常用的 skill</h2>
          {d.top_skills.length === 0 ? <p className="text-sm text-muted-foreground">本周没有 skill 使用记录。</p> : (
            <ul className="divide-y text-sm">{d.top_skills.map((s) => <li key={s.skill} className="flex items-center justify-between py-2"><span className="font-mono text-xs">{s.skill}</span><span className="tabular-nums text-muted-foreground">{s.count}</span></li>)}</ul>
          )}
        </CardContent></Card>
        <Card><CardContent className="p-5">
          <h2 className="mb-3 font-semibold">缓存命中</h2>
          <div className="text-2xl font-semibold tabular-nums">{d.cache_read_tokens.toLocaleString('zh-CN')}</div>
          <p className="mt-1 text-sm text-muted-foreground">本周从提示缓存读取的 token 数。数字越大，说明共享的 rule / skill 越稳定。</p>
        </CardContent></Card>
      </div>
      <Card className="mt-4"><CardContent className="p-5">
        <h2 className="mb-3 font-semibold">值得一看的会话</h2>
        <p className="mb-3 text-xs text-muted-foreground">工具调用多、或人工介入多的会话，通常意味着某个 skill 或 rule 需要改进。</p>
        {d.highlights.length === 0 ? <Empty>本周没有突出会话。</Empty> : (
          <Table>
            <TableHeader><TableRow><TableHead>成员</TableHead><TableHead>工具</TableHead><TableHead>开始</TableHead><TableHead>时长</TableHead><TableHead>调用</TableHead><TableHead>介入</TableHead></TableRow></TableHeader>
            <TableBody>{d.highlights.map((h, i) => <TableRow key={i}><TableCell>{h.email}</TableCell><TableCell><Badge>{h.tool}</Badge></TableCell><TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(h.started_at)}</TableCell><TableCell className="tabular-nums">{Math.round(h.duration_ms / 60000)} min</TableCell><TableCell className="tabular-nums">{h.tool_total}</TableCell><TableCell className="tabular-nums">{h.interventions > 0 ? <Badge variant="warning">{h.interventions}</Badge> : 0}</TableCell></TableRow>)}</TableBody>
          </Table>
        )}
      </CardContent></Card>
    </>
  )
}
