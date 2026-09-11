import { useState } from 'react'
import { Plus, Tag } from 'lucide-react'
import { toast } from 'sonner'
import { api, can, type Project, type Resource, type Team, type Version } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { navigate } from '@/navigation'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

export const KINDS = ['skill', 'rule', 'doc', 'agent', 'hook', 'mcp', 'env', 'claudemd', 'culture', 'policy', 'learning']

// 每种 kind 的入口文件与一份能通过发布口校验的模板。
export const ENTRY_FILE: Record<string, string> = {
  skill: 'SKILL.md', rule: 'RULE.md', doc: 'DOC.md', agent: 'AGENT.yaml', hook: 'HOOK.yaml', mcp: 'MCP.yaml',
  env: 'ENV.yaml', claudemd: 'CLAUDEMD.md', culture: 'CULTURE.md', policy: 'POLICY.yaml', learning: 'LEARNING.md',
}
export function templateFor(kind: string, name: string) {
  switch (kind) {
    case 'skill': return `---\nname: ${name}\ndescription: 这个技能做什么、什么时候该用它\n---\n\n在这里写下 Claude 应当遵循的指令。\n`
    case 'agent': return `name: ${name}\ndescription: 这个 subagent 的职责\nmodel: sonnet\ninstructions: |\n  在这里写下工作流程与交付格式。\n`
    case 'hook': return `id: ${name}\ndescription: 做什么\nevent: PostToolUse\nmatcher: Edit\ncommand: scripts/${name}.sh\ntimeout: 30\n`
    case 'mcp': return `name: ${name}\ndescription: 做什么\ntransport: http\nurl: https://mcp.example.com/${name}\n`
    case 'env': return `value: \ndescription: 这个变量做什么\n`
    case 'policy': return `enforced_rules: []\nhooks_auto_apply: true\nmcp_auto_apply: true\nmcp_allowed_hosts: []\nrecall_enabled: false\ncontribute_hint: true\nco_author: true\n`
    case 'culture': return `---\ncompany:\n  name: 公司名\n  mission: 一句话使命\n---\n\n团队的协作准则。\n`
    case 'learning': return `---\ntitle: ${name}\n---\n\n经验正文。\n`
    default: return `# ${name}\n\n正文。\n`
  }
}

function levelLabel(r: Resource) {
  if (r.level === 'org') return '组织'
  return `${r.level === 'team' ? '团队' : '项目'} · ${r.namespace}`
}

export function Resources({ id }: { id?: string }) {
  const [kind, setKind] = useState('')
  const [q, setQ] = useState('')
  const [level, setLevel] = useState('')
  const list = useAsync(() => api.resources({ kind, q, level }), [kind, q, level])
  const [creating, setCreating] = useState(false)
  if (id) return <ResourceDetail id={id} />

  return (
    <>
      <PageHeader
        title="内容库"
        description="所有已发布的资源，按层级落点解析后下发。改内容要经变更集：在这里新建，走审核后发布。"
        action={can('resource:write') && <Button onClick={() => setCreating(true)}><Plus />新建资源</Button>}
      />
      <div className="mb-4 flex flex-wrap gap-2">
        <Input placeholder="按名字或描述搜索" value={q} onChange={(e) => setQ(e.target.value)} className="w-64" />
        <Select value={kind || 'all'} onValueChange={(v) => setKind(v === 'all' ? '' : v)}>
          <SelectTrigger className="w-40"><SelectValue placeholder="类型" /></SelectTrigger>
          <SelectContent><SelectItem value="all">全部类型</SelectItem>{KINDS.map((k) => <SelectItem key={k} value={k}>{k}</SelectItem>)}</SelectContent>
        </Select>
        <Select value={level || 'all'} onValueChange={(v) => setLevel(v === 'all' ? '' : v)}>
          <SelectTrigger className="w-32"><SelectValue placeholder="层级" /></SelectTrigger>
          <SelectContent><SelectItem value="all">全部层级</SelectItem><SelectItem value="org">组织</SelectItem><SelectItem value="team">团队</SelectItem><SelectItem value="project">项目</SelectItem></SelectContent>
        </Select>
      </div>
      <Err msg={list.err} />
      {list.loading ? <TableSkeleton /> : !list.data?.items.length ? <Empty>没有匹配的资源。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>资源</TableHead><TableHead>类型</TableHead><TableHead>落点</TableHead><TableHead>版本</TableHead><TableHead>标签</TableHead><TableHead>发布时间</TableHead></TableRow></TableHeader>
            <TableBody>
              {list.data.items.map((r) => (
                <TableRow key={r.id} className="cursor-pointer" onClick={() => navigate('resources', r.id)}>
                  <TableCell><div className="font-medium">{r.name}</div><div className="text-xs text-muted-foreground">{r.description}</div></TableCell>
                  <TableCell><Badge variant="secondary">{r.kind}</Badge></TableCell>
                  <TableCell className="text-sm">{levelLabel(r)}{r.groups.length > 0 && <div className="text-xs text-muted-foreground">权限组：{r.groups.join(', ')}</div>}</TableCell>
                  <TableCell className="tabular-nums">{r.deleted ? <Badge variant="destructive">已删除 v{r.version}</Badge> : `v${r.version}`}</TableCell>
                  <TableCell className="text-xs">{r.tags.join(', ')}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(r.published_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      {creating && <CreateResource onClose={() => setCreating(false)} />}
    </>
  )
}

function ResourceDetail({ id }: { id: string }) {
  const { data, err, loading, reload } = useAsync(() => api.resource(id), [id])
  const [version, setVersion] = useState<(Version & { resource: Resource }) | null>(null)
  const [tags, setTags] = useState<string | null>(null)

  async function openVersion(v: Version) {
    try { setVersion(await api.resourceVersion(v.id)) } catch (e) { toast.error((e as Error).message) }
  }
  async function saveTags() {
    if (!data || tags === null) return
    try {
      await api.setTags(data.id, tags.split(',').map((t) => t.trim()).filter(Boolean))
      toast.success('标签已更新')
      setTags(null)
      reload()
    } catch (e) { toast.error((e as Error).message) }
  }

  if (loading) return <TableSkeleton />
  if (err || !data) return <Err msg={err || '不存在'} />
  return (
    <>
      <PageHeader
        title={`${data.kind} / ${data.name}`}
        description={<>{levelLabel(data)} · 当前 v{data.version}{data.deleted && '（已删除）'} · {data.description || '无描述'}</>}
        action={<div className="flex gap-2"><Button variant="outline" onClick={() => navigate('resources')}>返回列表</Button>{can('release:publish') && <Button variant="outline" onClick={() => setTags(data.tags.join(', '))}><Tag />标签</Button>}</div>}
      />
      <Card className="overflow-hidden">
        <Table>
          <TableHeader><TableRow><TableHead>版本</TableHead><TableHead>文件</TableHead><TableHead>发布者</TableHead><TableHead>发布时间</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>
            {data.versions.map((v) => (
              <TableRow key={v.id}>
                <TableCell className="tabular-nums">v{v.version}{v.deleted && <Badge variant="destructive" className="ml-2">删除</Badge>}{v.rollback_of_version && <Badge variant="secondary" className="ml-2">回滚自 v{v.rollback_of_version}</Badge>}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{v.files.map((f) => f.path).join(', ') || '—'}</TableCell>
                <TableCell>{v.published_by || '—'}</TableCell>
                <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(v.published_at)}</TableCell>
                <TableCell className="text-right">{!v.deleted && <Button variant="ghost" size="sm" onClick={() => openVersion(v)}>查看内容</Button>}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
      {version && (
        <Dialog open onOpenChange={() => setVersion(null)}>
          <DialogContent className="max-w-3xl">
            <DialogHeader><DialogTitle>{data.name} · v{version.version}</DialogTitle><DialogDescription>只读。要修改，请新建变更集。</DialogDescription></DialogHeader>
            <div className="max-h-[60vh] space-y-4 overflow-auto">
              {version.files.map((f) => (
                <div key={f.path}><div className="mb-1 font-mono text-xs text-muted-foreground">{f.path} · {f.size} B</div><pre className="overflow-auto rounded-md bg-muted p-3 text-xs">{version.contents?.[f.path] ?? '（文件过大，未内联）'}</pre></div>
              ))}
            </div>
          </DialogContent>
        </Dialog>
      )}
      {tags !== null && (
        <Dialog open onOpenChange={() => setTags(null)}>
          <DialogContent>
            <DialogHeader><DialogTitle>标签</DialogTitle><DialogDescription>逗号分隔。客户端按个人订阅过滤。</DialogDescription></DialogHeader>
            <Input value={tags} onChange={(e) => setTags(e.target.value)} />
            <DialogFooter><Button variant="outline" onClick={() => setTags(null)}>取消</Button><Button onClick={saveTags}>保存</Button></DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  )
}

// CreateResource 把「新建资源」翻译成一个只含一个 put 的变更集，并直接提交审核。
export function CreateResource({ onClose, presetProject }: { onClose: () => void; presetProject?: Project }) {
  const teams = useAsync(() => api.teams())
  const projects = useAsync(() => api.projects())
  const [kind, setKind] = useState('skill')
  const [name, setName] = useState('')
  const [level, setLevel] = useState(presetProject ? 'project' : 'org')
  const [owner, setOwner] = useState(presetProject?.id ?? '')
  const [content, setContent] = useState(templateFor('skill', 'my-skill'))
  const [title, setTitle] = useState('')
  const [fastTrack, setFastTrack] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  function pickKind(k: string) {
    setKind(k)
    if (k === 'culture' || k === 'policy') setName(k)
    setContent(templateFor(k, name || 'my-' + k))
  }

  async function submit() {
    setBusy(true)
    setErr('')
    try {
      const blob = await api.uploadBlob(content)
      const op = {
        op: 'put' as const, level, kind, name,
        team_id: level === 'team' ? owner : undefined, project_id: level === 'project' ? owner : undefined,
        files: [{ path: ENTRY_FILE[kind], sha256: blob.sha256, size: blob.size }],
      }
      const cs = await api.createChangeset({ title: title || `新增 ${kind}/${name}`, ops: [op], fast_track: fastTrack })
      const submitted = await api.submitChangeset(cs.id)
      toast.success(submitted.state === 'approved' ? '已创建并跳过审核，可以发布' : '已提交审核')
      onClose()
      navigate('changesets', cs.id)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-3xl">
        <DialogHeader><DialogTitle>新建资源</DialogTitle><DialogDescription>内容会先上传，再以变更集提交审核；有发布权限的人可勾选快速通道直接发布。</DialogDescription></DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2"><Label>类型</Label>
            <Select value={kind} onValueChange={pickKind}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{KINDS.map((k) => <SelectItem key={k} value={k}>{k}</SelectItem>)}</SelectContent></Select>
          </div>
          <div className="space-y-2"><Label htmlFor="res-name">名字</Label><Input id="res-name" value={name} onChange={(e) => setName(e.target.value)} disabled={kind === 'culture' || kind === 'policy'} placeholder={kind === 'env' ? 'API_BASE' : kind === 'doc' ? 'arch/overview.md' : 'my-skill'} /></div>
          <div className="space-y-2"><Label>层级</Label>
            <Select value={level} onValueChange={(v) => { setLevel(v); setOwner('') }}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="org">组织（全员）</SelectItem><SelectItem value="team">团队</SelectItem><SelectItem value="project">项目</SelectItem></SelectContent></Select>
          </div>
          {level !== 'org' && (
            <div className="space-y-2"><Label>{level === 'team' ? '团队' : '项目'}</Label>
              <Select value={owner} onValueChange={setOwner}><SelectTrigger><SelectValue placeholder="选择" /></SelectTrigger>
                <SelectContent>{(level === 'team' ? teams.data?.items ?? [] : projects.data?.items ?? []).map((x: Team | Project) => <SelectItem key={x.id} value={x.id}>{x.name}</SelectItem>)}</SelectContent>
              </Select>
            </div>
          )}
          <div className="space-y-2 sm:col-span-2"><Label htmlFor="res-content">{ENTRY_FILE[kind]}</Label><Textarea id="res-content" className="min-h-64 font-mono text-xs" value={content} onChange={(e) => setContent(e.target.value)} /></div>
          <div className="space-y-2 sm:col-span-2"><Label htmlFor="res-title">变更集标题</Label><Input id="res-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="留空则自动生成" /></div>
          {can('release:publish') && <label className="flex items-center gap-2 text-sm sm:col-span-2"><input type="checkbox" checked={fastTrack} onChange={(e) => setFastTrack(e.target.checked)} />快速通道：跳过审核，提交后可直接发布</label>}
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button disabled={busy || !name || (level !== 'org' && !owner)} onClick={submit}>{busy ? '提交中…' : '提交'}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
