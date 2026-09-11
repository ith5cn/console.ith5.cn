import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api, type Assignment } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

function subjectLabel(a: Assignment) {
  if (a.subject_type === 'org') return '全组织'
  return `${a.subject_type === 'user' ? '成员' : '项目'} · ${a.subject_name || a.subject_id}`
}

export function Assignments() {
  const { data, err, loading, reload } = useAsync(() => api.assignments())
  const [creating, setCreating] = useState(false)

  async function remove(id: string) {
    try {
      await api.deleteAssignment(id)
      toast.success('已撤销授权')
      reload()
    } catch (e) { toast.error((e as Error).message) }
  }

  return (
    <>
      <PageHeader
        title="授权策略"
        description="把权限组或单个资源授权给成员、项目或全组织。可设到期时间；到期后自动失效但保留记录以便审计。"
        action={<Button onClick={() => setCreating(true)}><Plus />新增授权</Button>}
      />
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>还没有授权。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>授权内容</TableHead><TableHead>授予</TableHead><TableHead>到期</TableHead><TableHead>创建</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {data.items.map((a) => (
                <TableRow key={a.id} className={a.expired ? 'opacity-60' : ''}>
                  <TableCell>{a.group_name ? <><Badge>权限组</Badge> <span className="font-medium">{a.group_name}</span></> : <><Badge variant="secondary">资源</Badge> <span className="font-medium">{a.bundle_name}</span></>}</TableCell>
                  <TableCell>{subjectLabel(a)}</TableCell>
                  <TableCell className="text-muted-foreground">{a.expires_at ? <>{fmtTime(a.expires_at)}{a.expired && <Badge variant="destructive" className="ml-2">已过期</Badge>}</> : '永久'}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(a.created_at)}</TableCell>
                  <TableCell className="text-right"><Button variant="ghost" size="sm" className="text-destructive" onClick={() => remove(a.id)}><Trash2 />撤销</Button></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      {creating && <CreateAssignment onClose={() => { setCreating(false); reload() }} />}
    </>
  )
}

function CreateAssignment({ onClose }: { onClose: () => void }) {
  const groups = useAsync(() => api.groups())
  const resources = useAsync(() => api.resources())
  const members = useAsync(() => api.members())
  const projects = useAsync(() => api.projects())
  const [targetKind, setTargetKind] = useState<'group' | 'bundle'>('group')
  const [target, setTarget] = useState('')
  const [subjectType, setSubjectType] = useState('user')
  const [subject, setSubject] = useState('')
  const [expires, setExpires] = useState('')
  const [err, setErr] = useState('')

  async function submit() {
    try {
      await api.createAssignment({
        group_id: targetKind === 'group' ? target : undefined,
        bundle_id: targetKind === 'bundle' ? target : undefined,
        subject_type: subjectType,
        subject_id: subjectType === 'org' ? undefined : subject,
        expires_at: expires ? new Date(expires).toISOString() : undefined,
      })
      toast.success('已授权')
      onClose()
    } catch (e) { setErr((e as Error).message) }
  }

  const ready = target && (subjectType === 'org' || subject)
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>新增授权</DialogTitle><DialogDescription>常规路径是授权权限组；直接授权单个资源用于一次性或临时需要。</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1"><Label>授权内容</Label>
            <div className="flex gap-2">
              <Select value={targetKind} onValueChange={(v) => { setTargetKind(v as 'group' | 'bundle'); setTarget('') }}><SelectTrigger className="w-32"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="group">权限组</SelectItem><SelectItem value="bundle">单个资源</SelectItem></SelectContent></Select>
              <Select value={target} onValueChange={setTarget}><SelectTrigger><SelectValue placeholder="选择" /></SelectTrigger>
                <SelectContent>
                  {targetKind === 'group'
                    ? groups.data?.items.filter((g) => !g.archived).map((g) => <SelectItem key={g.id} value={g.id}>{g.name}</SelectItem>)
                    : resources.data?.items.filter((r) => !r.deleted).map((r) => <SelectItem key={r.id} value={r.id}>{r.kind}/{r.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-1"><Label>授予</Label>
            <div className="flex gap-2">
              <Select value={subjectType} onValueChange={(v) => { setSubjectType(v); setSubject('') }}><SelectTrigger className="w-32"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="user">成员</SelectItem><SelectItem value="project">项目</SelectItem><SelectItem value="org">全组织</SelectItem></SelectContent></Select>
              {subjectType !== 'org' && (
                <Select value={subject} onValueChange={setSubject}><SelectTrigger><SelectValue placeholder="选择" /></SelectTrigger>
                  <SelectContent>
                    {subjectType === 'user'
                      ? members.data?.items.map((m) => <SelectItem key={m.user_id} value={m.user_id}>{m.email}</SelectItem>)
                      : projects.data?.items.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}
                  </SelectContent>
                </Select>
              )}
            </div>
          </div>
          <div className="space-y-1"><Label htmlFor="a-exp">到期时间（可选）</Label><Input id="a-exp" type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} /></div>
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={!ready}>授权</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
