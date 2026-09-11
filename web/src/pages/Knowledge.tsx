import { useState } from 'react'
import { ArrowUpRight, Plus } from 'lucide-react'
import { toast } from 'sonner'
import { api, can, getMe, type Learning } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { navigate } from '@/navigation'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

function Confidence({ value }: { value: number }) {
  const pct = Math.round(value * 100)
  const tone = value >= 0.7 ? 'bg-success' : value < 0.15 ? 'bg-destructive' : 'bg-primary'
  return <span className="inline-flex items-center gap-2 text-xs tabular-nums"><span className="h-1.5 w-16 overflow-hidden rounded-full bg-muted"><span className={`block h-full ${tone}`} style={{ width: `${pct}%` }} /></span>{pct}%</span>
}

export function Knowledge({ id }: { id?: string }) {
  const projects = useAsync(() => api.projects())
  const [q, setQ] = useState('')
  const [project, setProject] = useState('')
  const [status, setStatus] = useState('active')
  const list = useAsync(() => api.learnings({ q, project_id: project, status }), [q, project, status])
  const [sharing, setSharing] = useState(false)
  if (id) return <LearningDetail id={id} />
  return (
    <>
      <PageHeader title="知识库" description="成员分享的经验。召回与点赞由客户端上报，折算成置信度；低置信度的可归档，高置信度的可晋升为正式 rule / doc / skill。" action={<Button onClick={() => setSharing(true)}><Plus />分享经验</Button>} />
      <div className="mb-4 flex flex-wrap gap-2">
        <Input placeholder="搜索标题或正文" value={q} onChange={(e) => setQ(e.target.value)} className="w-64" />
        <Select value={project || 'all'} onValueChange={(v) => setProject(v === 'all' ? '' : v)}><SelectTrigger className="w-48"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部</SelectItem><SelectItem value="shared">组织共享</SelectItem>{projects.data?.items.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}</SelectContent></Select>
        <Select value={status} onValueChange={setStatus}><SelectTrigger className="w-32"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="active">在用</SelectItem><SelectItem value="archived">已归档</SelectItem><SelectItem value="all">全部</SelectItem></SelectContent></Select>
      </div>
      <Err msg={list.err} />
      {list.loading ? <TableSkeleton /> : !list.data?.items.length ? <Empty>没有匹配的经验。</Empty> : (
        <div className="grid gap-3">
          {list.data.items.map((l) => (
            <Card key={l.id} className="cursor-pointer transition-colors hover:bg-muted/50" onClick={() => navigate('knowledge', l.id)}>
              <CardContent className="p-4">
                <div className="flex flex-wrap items-center gap-2"><span className="font-medium">{l.title}</span><Badge variant="secondary">{l.namespace}</Badge>{l.archived && <Badge variant="destructive">已归档</Badge>}{l.tags.map((t) => <Badge key={t} variant="outline">{t}</Badge>)}</div>
                <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{l.excerpt}</p>
                <div className="mt-2 flex flex-wrap items-center gap-4 text-xs text-muted-foreground"><span>{l.author}</span><span>{fmtTime(l.published_at)}</span><span>召回 {l.recalled} · 点赞 {l.upvoted}</span><Confidence value={l.confidence} /></div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {sharing && <Contribute onClose={() => { setSharing(false); list.reload() }} />}
    </>
  )
}

function LearningDetail({ id }: { id: string }) {
  const { data, err, loading, reload } = useAsync(() => api.learning(id), [id])
  const [promoting, setPromoting] = useState(false)
  const me = getMe()!
  async function archive() {
    try { await api.archiveLearning(id); toast.success('已归档'); reload() } catch (e) { toast.error((e as Error).message) }
  }
  if (loading) return <TableSkeleton />
  if (err || !data) return <Err msg={err || '不存在'} />
  const l = data
  const canArchive = !l.archived && (l.author === me.account.email || can('release:publish'))
  return (
    <>
      <PageHeader title={l.title} description={<span className="flex flex-wrap items-center gap-3"><span>{l.author}</span><span>{fmtTime(l.published_at)}</span><span>召回 {l.recalled} · 点赞 {l.upvoted}</span><Confidence value={l.confidence} /></span>}
        action={<div className="flex gap-2"><Button variant="outline" onClick={() => navigate('knowledge')}>返回</Button>{can('resource:write') && !l.archived && <Button variant="outline" onClick={() => setPromoting(true)}><ArrowUpRight />晋升</Button>}{canArchive && <Button variant="outline" className="text-destructive" onClick={archive}>归档</Button>}</div>} />
      <Card><CardContent className="p-5"><pre className="whitespace-pre-wrap font-sans text-sm leading-6">{l.content}</pre></CardContent></Card>
      {promoting && <Promote learning={l} onClose={() => setPromoting(false)} />}
    </>
  )
}

function Contribute({ onClose }: { onClose: () => void }) {
  const projects = useAsync(() => api.projects())
  const [title, setTitle] = useState('')
  const [content, setContent] = useState('')
  const [project, setProject] = useState('')
  const [tags, setTags] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit() {
    setBusy(true)
    setErr('')
    try {
      const cs = await api.contribute({ title, content, project_id: project || undefined, tags: tags.split(',').map((t) => t.trim()).filter(Boolean) })
      toast.success(cs.state === 'published' ? '已发布' : '已提交审核')
      onClose()
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-2xl">
        <DialogHeader><DialogTitle>分享经验</DialogTitle><DialogDescription>正文会做密钥扫描，命中即拒绝。组织默认直接发布，除非策略里开启了审核。</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1"><Label htmlFor="l-title">标题</Label><Input id="l-title" value={title} onChange={(e) => setTitle(e.target.value)} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1"><Label>范围</Label><Select value={project || 'shared'} onValueChange={(v) => setProject(v === 'shared' ? '' : v)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="shared">组织共享</SelectItem>{projects.data?.items.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}</SelectContent></Select></div>
            <div className="space-y-1"><Label htmlFor="l-tags">标签（逗号分隔）</Label><Input id="l-tags" value={tags} onChange={(e) => setTags(e.target.value)} /></div>
          </div>
          <div className="space-y-1"><Label htmlFor="l-content">正文（Markdown）</Label><Textarea id="l-content" className="min-h-56 font-mono text-xs" value={content} onChange={(e) => setContent(e.target.value)} placeholder={'## 现象\n\n## 做法\n\n## 注意'} /></div>
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={busy || !title || !content}>{busy ? '提交中…' : '分享'}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Promote({ learning, onClose }: { learning: Learning; onClose: () => void }) {
  const [kind, setKind] = useState('rule')
  const [name, setName] = useState('')
  const [err, setErr] = useState('')
  async function submit() {
    try {
      const cs = await api.promoteLearning(learning.id, kind, name)
      toast.success('已生成草稿变更集')
      onClose()
      navigate('changesets', cs.id)
    } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>晋升为正式资源</DialogTitle><DialogDescription>生成一个草稿变更集，走正常审核；正文会去掉 frontmatter。</DialogDescription></DialogHeader>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1"><Label>类型</Label><Select value={kind} onValueChange={setKind}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="rule">rule</SelectItem><SelectItem value="doc">doc</SelectItem><SelectItem value="skill">skill</SelectItem></SelectContent></Select></div>
          <div className="space-y-1"><Label htmlFor="p-name">名字</Label><Input id="p-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={kind === 'doc' ? 'guides/x.md' : 'my-rule'} /></div>
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={!name}>生成变更集</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
