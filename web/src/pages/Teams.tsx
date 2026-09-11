import { useState } from 'react'
import { Plus } from 'lucide-react'
import { toast } from 'sonner'
import { api, can, getMe, type Member, type Team } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { navigate } from '@/navigation'
import { Empty, Err, PageHeader, TableSkeleton, useAsync } from '@/ui'

export function Teams({ id }: { id?: string }) {
  const { data, err, loading, reload } = useAsync(() => api.teams())
  const [creating, setCreating] = useState(false)
  if (id) return <TeamDetail id={id} />
  return (
    <>
      <PageHeader title="团队" description="项目可以挂在团队下。团队级资源发给团队成员；团队 admin 能管理团队及其项目。" action={can('membership:manage') && <Button onClick={() => setCreating(true)}><Plus />新建团队</Button>} />
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>还没有团队。</Empty> : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {data.items.map((t) => (
            <Card key={t.id} className="cursor-pointer transition-colors hover:bg-muted/50" onClick={() => navigate('teams', t.id)}>
              <CardContent className="p-4"><div className="font-medium">{t.name}{t.archived && <Badge variant="secondary" className="ml-2">已归档</Badge>}</div><div className="font-mono text-xs text-muted-foreground">{t.slug}</div></CardContent>
            </Card>
          ))}
        </div>
      )}
      {creating && <CreateScoped kind="team" onClose={() => { setCreating(false); reload() }} />}
    </>
  )
}

export function CreateScoped({ kind, onClose }: { kind: 'team' | 'project'; onClose: () => void }) {
  const teams = useAsync(() => (kind === 'project' ? api.teams() : Promise.resolve({ items: [] as Team[] })))
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [teamID, setTeamID] = useState('')
  const [err, setErr] = useState('')
  async function submit() {
    try {
      if (kind === 'team') await api.createTeam(name, slug)
      else await api.createProject(name, slug, teamID || undefined)
      toast.success('已创建')
      onClose()
    } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>新建{kind === 'team' ? '团队' : '项目'}</DialogTitle><DialogDescription>slug 会成为 teamai 端的命名空间目录名，建好后不能改。创建者自动成为 admin。</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1"><Label htmlFor="s-name">名称</Label><Input id="s-name" value={name} onChange={(e) => setName(e.target.value)} /></div>
          <div className="space-y-1"><Label htmlFor="s-slug">slug</Label><Input id="s-slug" value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="billing" /></div>
          {kind === 'project' && (
            <div className="space-y-1"><Label>所属团队（可选）</Label>
              <Select value={teamID || 'none'} onValueChange={(v) => setTeamID(v === 'none' ? '' : v)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">组织直属</SelectItem>{teams.data?.items.filter((t) => !t.archived).map((t) => <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>)}</SelectContent></Select>
            </div>
          )}
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={!name || !slug}>创建</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// MemberRoster 是团队与项目共用的成员表：加人、改角色、移出。
export function MemberRoster({ members, canManage, onPut, onDelete, extra }: {
  members: { user_id: string; email: string; name: string; role: string }[]
  canManage: boolean
  onPut: (userId: string, role: string) => Promise<unknown>
  onDelete: (userId: string) => Promise<unknown>
  extra?: React.ReactNode
}) {
  const all = useAsync(() => api.members())
  const [adding, setAdding] = useState('')
  async function run(fn: () => Promise<unknown>, ok: string) {
    try { await fn(); toast.success(ok) } catch (e) { toast.error((e as Error).message) }
  }
  const candidates = (all.data?.items ?? []).filter((m: Member) => m.status === 'active' && !members.some((x) => x.user_id === m.user_id))
  return (
    <Card><CardContent className="p-5">
      <div className="mb-3 flex items-center gap-3"><h2 className="font-semibold">成员</h2>{extra}</div>
      <ul className="divide-y text-sm">
        {members.map((m) => (
          <li key={m.user_id} className="flex items-center gap-3 py-2">
            <span className="min-w-0 flex-1 truncate">{m.name || m.email}<span className="ml-2 text-xs text-muted-foreground">{m.email}</span></span>
            {canManage ? (
              <>
                <Select value={m.role} onValueChange={(role) => run(() => onPut(m.user_id, role), '已更新')}><SelectTrigger className="h-8 w-28"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="admin">admin</SelectItem><SelectItem value="member">member</SelectItem></SelectContent></Select>
                <Button variant="ghost" size="sm" className="text-destructive" onClick={() => run(() => onDelete(m.user_id), '已移出')}>移出</Button>
              </>
            ) : <Badge variant="secondary">{m.role}</Badge>}
          </li>
        ))}
        {members.length === 0 && <li className="py-2 text-muted-foreground">还没有成员。</li>}
      </ul>
      {canManage && (
        <div className="mt-3 flex gap-2">
          <Select value={adding} onValueChange={setAdding}><SelectTrigger className="w-64"><SelectValue placeholder="添加成员" /></SelectTrigger><SelectContent>{candidates.map((m) => <SelectItem key={m.user_id} value={m.user_id}>{m.email}</SelectItem>)}</SelectContent></Select>
          <Button variant="outline" disabled={!adding} onClick={() => run(() => onPut(adding, 'member'), '已加入').then(() => setAdding(''))}>加入</Button>
        </div>
      )}
    </CardContent></Card>
  )
}

export function ReviewerEditor({ reviewers, canManage, onSave }: { reviewers: { user_id: string; email: string }[]; canManage: boolean; onSave: (ids: string[]) => Promise<unknown> }) {
  const all = useAsync(() => api.members())
  const [ids, setIds] = useState(reviewers.map((r) => r.user_id))
  return (
    <Card><CardContent className="p-5">
      <h2 className="mb-1 font-semibold">额外审核人</h2>
      <p className="mb-3 text-xs text-muted-foreground">admin 天然是审核人，这里指定更多。审核人不能审自己提交的变更。</p>
      <div className="max-h-48 overflow-auto rounded-md border">
        {(all.data?.items ?? []).filter((m) => m.status === 'active').map((m) => (
          <label key={m.user_id} className="flex items-center gap-2 border-b px-3 py-1.5 text-sm last:border-0"><input type="checkbox" disabled={!canManage} checked={ids.includes(m.user_id)} onChange={(e) => setIds((s) => e.target.checked ? [...s, m.user_id] : s.filter((x) => x !== m.user_id))} />{m.email}</label>
        ))}
      </div>
      {canManage && <Button className="mt-3" variant="outline" onClick={() => onSave(ids).then(() => toast.success('已保存'), (e) => toast.error((e as Error).message))}>保存审核人</Button>}
    </CardContent></Card>
  )
}

function TeamDetail({ id }: { id: string }) {
  const team = useAsync(() => api.team(id), [id])
  const reviewers = useAsync(() => api.teamReviewers(id), [id])
  // 组织 admin，或本团队的 admin，才能管成员与审核人
  const myID = getMe()!.membership.user_id
  const manage = can('membership:manage') || (team.data?.members ?? []).some((m) => m.role === 'admin' && m.user_id === myID)
  if (team.loading || reviewers.loading) return <TableSkeleton />
  if (team.err || !team.data) return <Err msg={team.err || '不存在'} />
  const t = team.data
  return (
    <>
      <PageHeader title={t.name} description={<span className="font-mono text-xs">{t.slug}</span>} action={<div className="flex gap-2"><Button variant="outline" onClick={() => navigate('teams')}>返回</Button>{manage && <Button variant="outline" onClick={() => api.updateTeam(t.id, t.name, !t.archived).then(() => { toast.success(t.archived ? '已恢复' : '已归档'); team.reload() })}>{t.archived ? '恢复' : '归档'}</Button>}</div>} />
      <div className="grid gap-4 lg:grid-cols-2">
        <MemberRoster members={t.members ?? []} canManage={manage} onPut={(u, r) => api.putTeamMember(t.id, u, r).then(team.reload)} onDelete={(u) => api.deleteTeamMember(t.id, u).then(team.reload)} />
        <ReviewerEditor reviewers={reviewers.data?.items ?? []} canManage={manage} onSave={(ids) => api.putTeamReviewers(t.id, ids)} />
      </div>
    </>
  )
}
