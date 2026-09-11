import { useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { api } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

type Tab = 'events' | 'distributions' | 'executions'

function Info({ children }: { children: React.ReactNode }) {
  return <div className="mb-4 flex gap-2 rounded-lg border bg-card px-4 py-3 text-sm text-muted-foreground"><ShieldAlert className="mt-0.5 size-4 shrink-0 text-primary" />{children}</div>
}

function Detail({ value }: { value?: Record<string, unknown> }) {
  if (!value || Object.keys(value).length === 0) return <span className="text-muted-foreground">—</span>
  return <code className="block max-w-md truncate rounded bg-muted px-1.5 py-0.5 text-xs" title={JSON.stringify(value)}>{JSON.stringify(value)}</code>
}

export function Audit() {
  const [tab, setTab] = useState<Tab>('events')
  const [type, setType] = useState('')
  return (
    <>
      <PageHeader title="审计" description="管理动作、内容分发与执行记录。只保留治理所需的元数据。" />
      <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}><TabsList className="mb-4"><TabsTrigger value="events">管理动作</TabsTrigger><TabsTrigger value="distributions">分发记录</TabsTrigger><TabsTrigger value="executions">执行记录</TabsTrigger></TabsList></Tabs>
      {tab === 'events' && <Events type={type} setType={setType} />}
      {tab === 'distributions' && <Distributions />}
      {tab === 'executions' && <Executions />}
    </>
  )
}

function Events({ type, setType }: { type: string; setType: (s: string) => void }) {
  const { data, err, loading } = useAsync(() => api.auditEvents(type || undefined), [type])
  return (
    <>
      <div className="mb-4"><Input placeholder="按事件类型过滤，如 changeset.published" value={type} onChange={(e) => setType(e.target.value)} className="w-80" /></div>
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>没有记录。</Empty> : (
        <Card className="overflow-hidden"><Table>
          <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>操作者</TableHead><TableHead>事件</TableHead><TableHead>对象</TableHead><TableHead>详情</TableHead></TableRow></TableHeader>
          <TableBody>{data.items.map((e) => <TableRow key={e.id}><TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(e.occurred_at)}</TableCell><TableCell>{e.actor_email || <span className="text-muted-foreground">系统</span>}</TableCell><TableCell><Badge>{e.type}</Badge></TableCell><TableCell className="font-mono text-xs">{e.target_type}/{e.target_id.slice(0, 8)}</TableCell><TableCell><Detail value={e.detail} /></TableCell></TableRow>)}</TableBody>
        </Table></Card>
      )}
    </>
  )
}

function Distributions() {
  const [action, setAction] = useState('')
  const { data, err, loading } = useAsync(() => api.distributions(action || undefined), [action])
  const tone = (a: string) => a === 'conflict_skipped' || a === 'failed' ? 'warning' : a === 'removed' ? 'destructive' : 'success'
  return (
    <>
      <Info>客户端每次同步后回报应用结果。conflict_skipped 表示目标路径被成员自有的同名内容占用，因此跳过。</Info>
      <Tabs value={action || 'all'} onValueChange={(v) => setAction(v === 'all' ? '' : v)}><TabsList className="mb-4"><TabsTrigger value="all">全部</TabsTrigger><TabsTrigger value="applied">已应用</TabsTrigger><TabsTrigger value="conflict_skipped">受阻</TabsTrigger><TabsTrigger value="removed">已移除</TabsTrigger></TabsList></Tabs>
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>没有记录。</Empty> : (
        <Card className="overflow-hidden"><Table>
          <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>成员 / 设备</TableHead><TableHead>内容</TableHead><TableHead>动作</TableHead><TableHead>详情</TableHead></TableRow></TableHeader>
          <TableBody>{data.items.map((d, i) => <TableRow key={i}><TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(d.occurred_at)}</TableCell><TableCell>{d.email}<div className="font-mono text-xs text-muted-foreground">{d.hostname || '—'}</div></TableCell><TableCell>{d.kind}/{d.name}{d.version ? <span className="ml-1 font-mono text-xs text-muted-foreground">v{d.version}</span> : null}</TableCell><TableCell><Badge variant={tone(d.action)}>{d.action}</Badge></TableCell><TableCell><Detail value={d.detail} /></TableCell></TableRow>)}</TableBody>
        </Table></Card>
      )}
    </>
  )
}

function Executions() {
  const { data, err, loading } = useAsync(() => api.executions())
  return (
    <>
      <Info>只记录工具、仓库和文件等元数据；文件内容、diff、完整命令和提示词一律不上报。</Info>
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>没有记录。</Empty> : (
        <Card className="overflow-hidden"><Table>
          <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>成员 / 设备</TableHead><TableHead>工具</TableHead><TableHead>摘要</TableHead></TableRow></TableHeader>
          <TableBody>{data.items.map((e, i) => <TableRow key={i}><TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(e.occurred_at)}</TableCell><TableCell>{e.email}<div className="font-mono text-xs text-muted-foreground">{e.hostname || '—'}</div></TableCell><TableCell><Badge>{e.tool_name || e.event_type}</Badge></TableCell><TableCell><Detail value={e.summary} /></TableCell></TableRow>)}</TableBody>
        </Table></Card>
      )}
    </>
  )
}
