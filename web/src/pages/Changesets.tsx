import { useState } from 'react'
import { toast } from 'sonner'
import { api, can, getMe, type Changeset, type DiffItem } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { navigate } from '@/navigation'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

const STATE_LABEL: Record<string, string> = {
  draft: '草稿', in_review: '审核中', approved: '已批准', published: '已发布', rejected: '已拒绝', cancelled: '已取消',
}
const STATE_VARIANT: Record<string, 'secondary' | 'default' | 'success' | 'destructive'> = {
  draft: 'secondary', in_review: 'default', approved: 'success', published: 'success', rejected: 'destructive', cancelled: 'secondary',
}

export function StateBadge({ state }: { state: string }) {
  return <Badge variant={STATE_VARIANT[state] ?? 'secondary'}>{STATE_LABEL[state] ?? state}</Badge>
}

export function Changesets({ id }: { id?: string }) {
  const [view, setView] = useState<'mine' | 'review' | 'all'>(id === 'review' ? 'review' : 'mine')
  const list = useAsync(() => api.changesets(view), [view])
  if (id && id !== 'review') return <ChangesetDetail id={id} />

  return (
    <>
      <PageHeader title="变更集" description="对内容的一组改动：草稿 → 审核 → 批准 → 发布。发布是原子的，客户端只会看到旧版本或新版本。" />
      <Tabs value={view} onValueChange={(v) => setView(v as typeof view)} className="mb-4">
        <TabsList><TabsTrigger value="mine">我的</TabsTrigger><TabsTrigger value="review">待我审核</TabsTrigger>{can('audit:read') && <TabsTrigger value="all">全部</TabsTrigger>}</TabsList>
      </Tabs>
      <Err msg={list.err} />
      {list.loading ? <TableSkeleton /> : !list.data?.items.length ? <Empty>这里还没有变更集。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>标题</TableHead><TableHead>状态</TableHead><TableHead>操作数</TableHead><TableHead>作者</TableHead><TableHead>更新时间</TableHead></TableRow></TableHeader>
            <TableBody>
              {list.data.items.map((cs) => (
                <TableRow key={cs.id} className="cursor-pointer" onClick={() => navigate('changesets', cs.id)}>
                  <TableCell className="font-medium">{cs.title}{cs.fast_track && <Badge variant="secondary" className="ml-2">快速通道</Badge>}</TableCell>
                  <TableCell><StateBadge state={cs.state} /></TableCell>
                  <TableCell className="tabular-nums">{cs.ops.length}</TableCell>
                  <TableCell>{cs.author_email}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(cs.updated_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </>
  )
}

function ChangesetDetail({ id }: { id: string }) {
  const { data, err, loading, reload } = useAsync(() => api.changeset(id), [id])
  const diff = useAsync(() => api.changesetDiff(id), [id])
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const me = getMe()!

  async function act(fn: () => Promise<unknown>, ok: string) {
    setBusy(true)
    try {
      await fn()
      toast.success(ok)
      reload()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <TableSkeleton />
  if (err || !data) return <Err msg={err || '不存在'} />
  const cs: Changeset = data
  const mine = cs.author_email === me.account.email
  const canReview = cs.state === 'in_review' && !mine && cs.ops.every((op) => can('review:decide') || true)
  const canPublish = cs.state === 'approved' && can('release:publish')

  return (
    <>
      <PageHeader
        title={cs.title}
        description={<span className="flex flex-wrap items-center gap-2"><StateBadge state={cs.state} />{cs.fast_track && <Badge variant="secondary">快速通道</Badge>}<span>{cs.author_email} · {fmtTime(cs.created_at)}</span>{cs.description && <span className="text-foreground">{cs.description}</span>}</span>}
        action={<Button variant="outline" onClick={() => navigate('changesets')}>返回列表</Button>}
      />
      <Card className="overflow-hidden">
        <Table>
          <TableHeader><TableRow><TableHead>操作</TableHead><TableHead>资源</TableHead><TableHead>落点</TableHead><TableHead>变化</TableHead><TableHead>文件</TableHead></TableRow></TableHeader>
          <TableBody>
            {(diff.data?.items ?? []).map((d: DiffItem, i) => (
              <TableRow key={i}>
                <TableCell><Badge variant={d.op === 'delete' ? 'destructive' : 'secondary'}>{d.op}</Badge></TableCell>
                <TableCell className="font-medium">{d.kind} / {d.name}</TableCell>
                <TableCell className="font-mono text-xs">{d.scope_key}</TableCell>
                <TableCell>{d.change === 'create' ? '新增' : d.change === 'update' ? `更新（当前 v${d.current_version}）` : '删除'}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{d.files?.map((f) => f.path).join(', ') ?? '—'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>

      {cs.reviews.length > 0 && (
        <Card className="mt-4"><CardContent className="p-5">
          <h2 className="mb-2 font-semibold">审核记录</h2>
          <ul className="divide-y text-sm">
            {cs.reviews.map((r) => (
              <li key={r.id} className={`flex flex-wrap items-center gap-2 py-2 ${r.superseded ? 'text-muted-foreground line-through' : ''}`}>
                <Badge variant={r.decision === 'approve' ? 'success' : r.decision === 'reject' ? 'destructive' : 'secondary'}>{r.decision}</Badge>
                <span>{r.reviewer_email}</span>
                {r.comment && <span className="text-muted-foreground">「{r.comment}」</span>}
                <span className="ml-auto text-xs text-muted-foreground">{fmtTime(r.created_at)}</span>
              </li>
            ))}
          </ul>
        </CardContent></Card>
      )}

      <Card className="mt-4"><CardContent className="flex flex-wrap items-end gap-3 p-5">
        {cs.state === 'draft' && mine && <Button disabled={busy} onClick={() => act(() => api.submitChangeset(cs.id), '已提交')}>提交审核</Button>}
        {canReview && (
          <>
            <div className="min-w-64 flex-1 space-y-1"><label className="text-sm" htmlFor="review-comment">审核意见</label><Textarea id="review-comment" value={comment} onChange={(e) => setComment(e.target.value)} rows={2} /></div>
            <Button disabled={busy} onClick={() => act(() => api.reviewChangeset(cs.id, 'approve', cs.submitted_digest!, comment), '已批准')}>批准</Button>
            <Button disabled={busy} variant="outline" onClick={() => act(() => api.reviewChangeset(cs.id, 'request_changes', cs.submitted_digest!, comment), '已退回')}>退回修改</Button>
            <Button disabled={busy} variant="destructive" onClick={() => act(() => api.reviewChangeset(cs.id, 'reject', cs.submitted_digest!, comment), '已拒绝')}>拒绝</Button>
          </>
        )}
        {canPublish && <Button disabled={busy} onClick={() => act(() => api.publishChangeset(cs.id), '已发布')}>发布</Button>}
        {!['published', 'rejected', 'cancelled'].includes(cs.state) && (mine || can('membership:manage')) && (
          <Button disabled={busy} variant="ghost" className="text-destructive" onClick={() => act(() => api.cancelChangeset(cs.id), '已取消')}>取消变更集</Button>
        )}
        {cs.state === 'published' && cs.release_id && <Button variant="outline" onClick={() => navigate('releases', cs.release_id)}>查看发布</Button>}
        {cs.state === 'in_review' && mine && <span className="text-sm text-muted-foreground">等待审核。你不能审核自己的变更。</span>}
      </CardContent></Card>
    </>
  )
}
