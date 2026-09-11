import { useState } from 'react'
import { KeyRound, Laptop, Plus, Ticket } from 'lucide-react'
import { toast } from 'sonner'
import { api, can, getMe, type Device, type Enrollment, type Member } from '@/api'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

export function Members() {
  const manage = can('membership:manage')
  return (
    <>
      <PageHeader title="成员与设备" description="停用会在一个事务里撤销该成员的全部设备、刷新凭据与绑定；本机内容依赖设备下次同步清理，不承诺远程擦除。" />
      <Tabs defaultValue="members">
        <TabsList className="mb-4"><TabsTrigger value="members">成员</TabsTrigger><TabsTrigger value="devices">设备</TabsTrigger>{manage && <TabsTrigger value="enrollments">接入码</TabsTrigger>}</TabsList>
        <TabsContent value="members"><MemberList /></TabsContent>
        <TabsContent value="devices"><DeviceList /></TabsContent>
        {manage && <TabsContent value="enrollments"><EnrollmentList /></TabsContent>}
      </Tabs>
    </>
  )
}

function MemberList() {
  const { data, err, loading, reload } = useAsync(() => api.members())
  const [creating, setCreating] = useState(false)
  const [resetting, setResetting] = useState<Member | null>(null)
  const me = getMe()!
  const manage = can('membership:manage')
  const isOwner = me.membership.role === 'owner'

  async function run(fn: () => Promise<unknown>, ok: string) {
    try { await fn(); toast.success(ok); reload() } catch (e) { toast.error((e as Error).message) }
  }

  return (
    <>
      {manage && <div className="mb-3 flex justify-end"><Button onClick={() => setCreating(true)}><Plus />邀请成员</Button></div>}
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>没有成员。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>成员</TableHead><TableHead>角色</TableHead><TableHead>状态</TableHead><TableHead>设备</TableHead><TableHead>最后活跃</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {data.items.map((m) => (
                <TableRow key={m.user_id}>
                  <TableCell><div className="font-medium">{m.name || m.email}</div>{m.name && <div className="text-xs text-muted-foreground">{m.email}</div>}</TableCell>
                  <TableCell>
                    {manage && m.user_id !== me.membership.user_id && (isOwner || m.role !== 'owner') ? (
                      <Select value={m.role} onValueChange={(role) => run(() => api.setMemberRole(m.user_id, role), '角色已更新')}>
                        <SelectTrigger className="h-8 w-28"><SelectValue /></SelectTrigger>
                        <SelectContent>{isOwner && <SelectItem value="owner">owner</SelectItem>}<SelectItem value="admin">admin</SelectItem><SelectItem value="member">member</SelectItem><SelectItem value="viewer">viewer</SelectItem></SelectContent>
                      </Select>
                    ) : <Badge>{m.role}</Badge>}
                  </TableCell>
                  <TableCell>{m.status === 'active' ? <Badge variant="success">正常</Badge> : <Badge variant="destructive">已停用</Badge>}</TableCell>
                  <TableCell className="tabular-nums">{m.machines} 台</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(m.last_seen_at)}</TableCell>
                  <TableCell>
                    {manage && m.user_id !== me.membership.user_id && (
                      <div className="flex justify-end gap-1">
                        <Button variant="ghost" size="sm" onClick={() => setResetting(m)}><KeyRound />重置密码</Button>
                        <AlertDialog>
                          <AlertDialogTrigger asChild><Button variant="ghost" size="sm" className={m.status === 'active' ? 'text-destructive' : ''}>{m.status === 'active' ? '停用' : '恢复'}</Button></AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader><AlertDialogTitle>{m.status === 'active' ? '停用该成员？' : '恢复该成员？'}</AlertDialogTitle><AlertDialogDescription>{m.status === 'active' ? '立即撤销全部设备与凭据；本机内容在设备下次同步时清理。' : '恢复后需要重新登录设备。'}</AlertDialogDescription></AlertDialogHeader>
                            <AlertDialogFooter><AlertDialogCancel>取消</AlertDialogCancel><AlertDialogAction onClick={() => run(() => api.setMemberStatus(m.user_id, m.status === 'active' ? 'suspended' : 'active'), '已更新')}>确认</AlertDialogAction></AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      {creating && <CreateMember isOwner={isOwner} onClose={() => { setCreating(false); reload() }} />}
      {resetting && <ResetPassword member={resetting} onClose={() => setResetting(null)} />}
    </>
  )
}

function CreateMember({ isOwner, onClose }: { isOwner: boolean; onClose: () => void }) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState('member')
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  async function submit() {
    try { await api.createMember(email, name, role, pw); toast.success('已邀请'); onClose() } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>邀请成员</DialogTitle><DialogDescription>没有邮件设施，初始密码线下交给员工；员工首次登录后应自行修改。</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1"><Label htmlFor="m-email">邮箱</Label><Input id="m-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></div>
          <div className="space-y-1"><Label htmlFor="m-name">姓名</Label><Input id="m-name" value={name} onChange={(e) => setName(e.target.value)} /></div>
          <div className="space-y-1"><Label>角色</Label><Select value={role} onValueChange={setRole}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{isOwner && <SelectItem value="owner">owner</SelectItem>}<SelectItem value="admin">admin</SelectItem><SelectItem value="member">member</SelectItem><SelectItem value="viewer">viewer（只读）</SelectItem></SelectContent></Select></div>
          <div className="space-y-1"><Label htmlFor="m-pw">初始密码（至少 12 位）</Label><Input id="m-pw" type="password" value={pw} onChange={(e) => setPw(e.target.value)} /></div>
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={!email || pw.length < 12}>邀请</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ResetPassword({ member, onClose }: { member: Member; onClose: () => void }) {
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  async function submit() {
    try { await api.resetPassword(member.user_id, pw); toast.success('密码已重置'); onClose() } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>重置 {member.email} 的密码</DialogTitle><DialogDescription>旧密码立即失效；已登录的设备不受影响。</DialogDescription></DialogHeader>
        <div className="space-y-1"><Label htmlFor="r-pw">新密码（至少 12 位）</Label><Input id="r-pw" type="password" value={pw} onChange={(e) => setPw(e.target.value)} /></div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={pw.length < 12}>重置</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DeviceList() {
  const manage = can('device:manage')
  const { data, err, loading, reload } = useAsync(() => api.devices(manage ? 'all' : undefined))
  async function revoke(d: Device) {
    try { await api.revokeDevice(d.id); toast.success('设备已撤销'); reload() } catch (e) { toast.error((e as Error).message) }
  }
  return (
    <>
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>没有设备。员工用 CLI 登录后会出现在这里。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>设备</TableHead><TableHead>成员</TableHead><TableHead>系统</TableHead><TableHead>最后活跃</TableHead><TableHead>状态</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {data.items.map((d) => (
                <TableRow key={d.id} className={d.revoked_at ? 'opacity-60' : ''}>
                  <TableCell className="font-medium"><Laptop className="mr-2 inline size-4 text-muted-foreground" />{d.hostname || d.id.slice(0, 8)}</TableCell>
                  <TableCell>{d.email}</TableCell>
                  <TableCell>{d.os || '—'}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(d.last_seen_at)}</TableCell>
                  <TableCell>{d.revoked_at ? <Badge variant="destructive">已撤销</Badge> : <Badge variant="success">正常</Badge>}</TableCell>
                  <TableCell className="text-right">{!d.revoked_at && <Button variant="ghost" size="sm" className="text-destructive" onClick={() => revoke(d)}>撤销</Button>}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </>
  )
}

function EnrollmentList() {
  const { data, err, loading, reload } = useAsync(() => api.enrollments())
  const projects = useAsync(() => api.projects())
  const [creating, setCreating] = useState(false)
  const [issued, setIssued] = useState<Enrollment | null>(null)
  async function revoke(id: string) {
    try { await api.revokeEnrollment(id); toast.success('已撤销'); reload() } catch (e) { toast.error((e as Error).message) }
  }
  const projectName = (id: string) => projects.data?.items.find((p) => p.id === id)?.name ?? id.slice(0, 8)
  return (
    <>
      <div className="mb-3 flex justify-end"><Button onClick={() => setCreating(true)}><Ticket />签发接入码</Button></div>
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>还没有接入码。签发一个，员工就能不了解 Git 地接入。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>限定项目</TableHead><TableHead>使用</TableHead><TableHead>到期</TableHead><TableHead>状态</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {data.items.map((e) => {
                const dead = !!e.revoked_at || new Date(e.expires_at) < new Date() || e.used_count >= e.max_uses
                return (
                  <TableRow key={e.id} className={dead ? 'opacity-60' : ''}>
                    <TableCell>{e.project_ids.map(projectName).join(', ')}</TableCell>
                    <TableCell className="tabular-nums">{e.used_count} / {e.max_uses}</TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(e.expires_at)}</TableCell>
                    <TableCell>{e.revoked_at ? <Badge variant="destructive">已撤销</Badge> : dead ? <Badge variant="secondary">已失效</Badge> : <Badge variant="success">可用</Badge>}</TableCell>
                    <TableCell className="text-right">{!dead && <Button variant="ghost" size="sm" className="text-destructive" onClick={() => revoke(e.id)}>撤销</Button>}</TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </Card>
      )}
      {creating && <CreateEnrollment onDone={(e) => { setCreating(false); setIssued(e); reload() }} onClose={() => setCreating(false)} />}
      {issued && (
        <Dialog open onOpenChange={() => setIssued(null)}>
          <DialogContent>
            <DialogHeader><DialogTitle>接入码已签发</DialogTitle><DialogDescription>只显示这一次。把下面这条命令发给员工。</DialogDescription></DialogHeader>
            <pre className="overflow-auto rounded-md bg-muted p-3 text-xs">{issued.command}</pre>
            <DialogFooter><Button onClick={() => { navigator.clipboard.writeText(issued.command ?? ''); toast.success('已复制') }}>复制命令</Button></DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  )
}

function CreateEnrollment({ onDone, onClose }: { onDone: (e: Enrollment) => void; onClose: () => void }) {
  const projects = useAsync(() => api.projects())
  const [selected, setSelected] = useState<string[]>([])
  const [ttl, setTtl] = useState(168)
  const [max, setMax] = useState(1)
  const [err, setErr] = useState('')
  async function submit() {
    try { onDone(await api.createEnrollment(selected, ttl, max)) } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>签发接入码</DialogTitle><DialogDescription>接入码不授予成员身份：员工必须本来就是这些项目的成员，批准设备时会检查。</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1"><Label>限定项目</Label>
            <div className="max-h-40 overflow-auto rounded-md border">
              {(projects.data?.items ?? []).map((p) => (
                <label key={p.id} className="flex cursor-pointer items-center gap-2 border-b px-3 py-2 text-sm last:border-0"><input type="checkbox" checked={selected.includes(p.id)} onChange={(e) => setSelected((s) => e.target.checked ? [...s, p.id] : s.filter((x) => x !== p.id))} />{p.name}</label>
              ))}
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1"><Label htmlFor="e-ttl">有效期（小时）</Label><Input id="e-ttl" type="number" min={1} value={ttl} onChange={(e) => setTtl(Number(e.target.value))} /></div>
            <div className="space-y-1"><Label htmlFor="e-max">可用次数</Label><Input id="e-max" type="number" min={1} value={max} onChange={(e) => setMax(Number(e.target.value))} /></div>
          </div>
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={selected.length === 0}>签发</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
