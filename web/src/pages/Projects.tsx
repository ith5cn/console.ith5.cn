import { useState } from 'react'
import { Plus } from 'lucide-react'
import { toast } from 'sonner'
import { api, can, getMe } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { navigate } from '@/navigation'
import { CreateScoped, MemberRoster, ReviewerEditor } from '@/pages/Teams'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

export function Projects({ id }: { id?: string }) {
  const { data, err, loading, reload } = useAsync(() => api.projects())
  const teams = useAsync(() => api.teams())
  const [creating, setCreating] = useState(false)
  if (id) return <ProjectDetail id={id} />
  const teamName = (tid?: string) => teams.data?.items.find((t) => t.id === tid)?.name
  return (
    <>
      <PageHeader title="项目" description="项目是资源与经验的隔离边界。项目级资源只发给项目成员，项目内的 learnings 只对项目可见。" action={can('membership:manage') && <Button onClick={() => setCreating(true)}><Plus />新建项目</Button>} />
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>你还不属于任何项目。</Empty> : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {data.items.map((p) => (
            <Card key={p.id} className="cursor-pointer transition-colors hover:bg-muted/50" onClick={() => navigate('projects', p.id)}>
              <CardContent className="p-4"><div className="font-medium">{p.name}{p.archived && <Badge variant="secondary" className="ml-2">已归档</Badge>}</div><div className="font-mono text-xs text-muted-foreground">{p.slug}{p.team_id && ` · ${teamName(p.team_id) ?? '团队'}`}</div></CardContent>
            </Card>
          ))}
        </div>
      )}
      {creating && <CreateScoped kind="project" onClose={() => { setCreating(false); reload() }} />}
    </>
  )
}

function ProjectDetail({ id }: { id: string }) {
  const project = useAsync(() => api.project(id), [id])
  const reviewers = useAsync(() => api.projectReviewers(id), [id])
  const bindings = useAsync(() => (can('device:manage') ? api.bindings('all') : api.bindings()), [id])
  const myID = getMe()!.membership.user_id
  if (project.loading || reviewers.loading) return <TableSkeleton />
  if (project.err || !project.data) return <Err msg={project.err || '不存在'} />
  const p = project.data
  const manage = can('membership:manage') || (p.members ?? []).some((m) => m.role === 'admin' && m.user_id === myID)
  const bound = (bindings.data?.items ?? []).filter((b) => b.project_ids.includes(p.id) && b.state === 'active')
  return (
    <>
      <PageHeader title={p.name} description={<span className="font-mono text-xs">{p.slug}</span>} action={<div className="flex gap-2"><Button variant="outline" onClick={() => navigate('projects')}>返回</Button>{manage && <Button variant="outline" onClick={() => api.updateProject(p.id, p.name, p.team_id ?? '', !p.archived).then(() => { toast.success(p.archived ? '已恢复' : '已归档'); project.reload() }, (e) => toast.error((e as Error).message))}>{p.archived ? '恢复' : '归档'}</Button>}</div>} />
      <div className="grid gap-4 lg:grid-cols-2">
        <MemberRoster members={p.members ?? []} canManage={manage} onPut={(u, r) => api.putProjectMember(p.id, u, r).then(project.reload)} onDelete={(u) => api.deleteProjectMember(p.id, u).then(project.reload)} />
        <ReviewerEditor reviewers={reviewers.data?.items ?? []} canManage={manage} onSave={(ids) => api.putProjectReviewers(p.id, ids)} />
      </div>
      <Card className="mt-4"><CardContent className="p-5">
        <h2 className="mb-3 font-semibold">同步到这个项目的设备</h2>
        {bound.length === 0 ? <p className="text-sm text-muted-foreground">还没有设备绑定这个项目。</p> : (
          <Table>
            <TableHeader><TableRow><TableHead>目录</TableHead><TableHead>已应用版本</TableHead><TableHead>最近同步</TableHead></TableRow></TableHeader>
            <TableBody>{bound.map((b) => <TableRow key={b.id}><TableCell>{b.display_name || b.workspace_id.slice(0, 8)}</TableCell><TableCell className="font-mono text-xs">{b.applied_revision?.slice(0, 19) || '—'}</TableCell><TableCell className="text-muted-foreground">{fmtTime(b.last_sync_at)}</TableCell></TableRow>)}</TableBody>
          </Table>
        )}
      </CardContent></Card>
    </>
  )
}
