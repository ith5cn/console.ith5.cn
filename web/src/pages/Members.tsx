import { useState } from 'react'
import { KeyRound, Laptop, Plus, UserRoundCheck } from 'lucide-react'
import { toast } from 'sonner'
import { api, getUser, type Member } from '../api'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '../ui'

export function Members() {
  const { data, err, loading, reload } = useAsync(() => api.listMembers())
  const [creating, setCreating] = useState(false)
  const [resetting, setResetting] = useState<Member | null>(null)
  const isOwner = getUser()?.role === 'owner'

  async function toggle(id: string, status: string) {
    const next = status === 'active' ? 'suspended' : 'active'
    try {
      await api.setMemberStatus(id, next)
      toast.success(next === 'active' ? '成员已恢复' : '成员已停用')
      reload()
    } catch (error) {
      toast.error((error as Error).message)
    }
  }

  const active = data?.members.filter((member) => member.status === 'active').length ?? 0
  const machines = data?.members.reduce((sum, member) => sum + member.machines, 0) ?? 0

  return <>
    <PageHeader
      title="成员与设备"
      description="停用会切断后续更新，并在设备下次同步时清理内容；离线设备不会被立即擦除。"
      action={<Button onClick={() => { setResetting(null); setCreating(true) }}><Plus />新增成员</Button>}
    />
    {!loading && data && <div className="mb-4 grid gap-3 sm:grid-cols-2">
      <Card><CardContent className="flex items-center gap-3 p-4"><span className="grid size-10 place-items-center rounded-lg bg-accent text-accent-foreground"><UserRoundCheck className="size-5" /></span><div><div className="text-xl font-semibold tabular-nums">{active}</div><div className="text-xs text-muted-foreground">活跃成员</div></div></CardContent></Card>
      <Card><CardContent className="flex items-center gap-3 p-4"><span className="grid size-10 place-items-center rounded-lg bg-muted text-muted-foreground"><Laptop className="size-5" /></span><div><div className="text-xl font-semibold tabular-nums">{machines}</div><div className="text-xs text-muted-foreground">已绑定设备</div></div></CardContent></Card>
    </div>}
    <Err msg={err} />
    {loading ? <TableSkeleton /> : !data?.members.length ? <Empty>没有成员，点击“新增成员”创建首个账号。</Empty> : <Card className="overflow-hidden"><Table><TableHeader><TableRow><TableHead>成员</TableHead><TableHead>角色</TableHead><TableHead>状态</TableHead><TableHead>设备</TableHead><TableHead>最后活跃</TableHead><TableHead /></TableRow></TableHeader><TableBody>{data.members.map((member) => <TableRow key={member.id}>
      <TableCell><div className="font-medium">{member.name || member.email}</div>{member.name && <div className="text-xs text-muted-foreground">{member.email}</div>}</TableCell>
      <TableCell><Badge>{member.role}</Badge></TableCell>
      <TableCell>{member.status === 'active' ? <Badge variant="success"><span className="size-1.5 rounded-full bg-success" />正常</Badge> : <Badge variant="destructive">已停用</Badge>}</TableCell>
      <TableCell className="tabular-nums">{member.machines} 台</TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">{fmtTime(member.last_seen_at)}</TableCell>
      <TableCell><div className="flex justify-end gap-1">
        {(isOwner || member.role === 'member') && <Button variant="ghost" size="sm" onClick={() => { setCreating(false); setResetting(member) }}><KeyRound />重置密码</Button>}
        <AlertDialog><AlertDialogTrigger asChild><Button variant="ghost" size="sm" className={member.status === 'active' ? 'text-destructive hover:text-destructive' : ''}>{member.status === 'active' ? '停用' : '恢复'}</Button></AlertDialogTrigger><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{member.status === 'active' ? '停用该成员？' : '恢复该成员？'}</AlertDialogTitle><AlertDialogDescription>{member.status === 'active' ? '成员下次同步将停止获取更新并清理已分发内容。此操作依赖设备再次同步。' : '恢复后，该成员会在下次同步重新获得当前有效授权。'}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>取消</AlertDialogCancel><AlertDialogAction onClick={() => toggle(member.id, member.status)}>{member.status === 'active' ? '确认停用' : '确认恢复'}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
      </div></TableCell>
    </TableRow>)}</TableBody></Table></Card>}
    {creating && <CreateMember isOwner={isOwner} onDone={() => { setCreating(false); toast.success('成员已创建'); reload() }} onCancel={() => setCreating(false)} />}
    {resetting && <ResetPassword member={resetting} onDone={() => { setResetting(null); toast.success('密码已重置') }} onCancel={() => setResetting(null)} />}
  </>
}

function CreateMember({ isOwner, onDone, onCancel }: { isOwner: boolean; onDone: () => void; onCancel: () => void }) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState('member')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await api.createMember(email, name, role, password)
      onDone()
    } catch (error) {
      setErr((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return <Dialog open onOpenChange={(open) => { if (!open) onCancel() }}><DialogContent><form onSubmit={submit} className="grid gap-5">
    <DialogHeader><DialogTitle>新增成员</DialogTitle><DialogDescription>创建登录账号并设置初始密码。系统不会通过邮件发送凭据。</DialogDescription></DialogHeader>
    <div className="grid gap-2"><Label htmlFor="member-email">邮箱（登录账号）</Label><Input id="member-email" type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="off" required /></div>
    <div className="grid gap-2"><Label htmlFor="member-name">姓名</Label><Input id="member-name" value={name} onChange={(event) => setName(event.target.value)} /></div>
    <div className="grid gap-2"><Label htmlFor="member-role">角色</Label><Select value={role} onValueChange={setRole}><SelectTrigger id="member-role"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="member">member · 只用客户端</SelectItem>{isOwner && <><SelectItem value="viewer">viewer · 演示只读</SelectItem><SelectItem value="admin">admin · 可进入管理后台</SelectItem></>}</SelectContent></Select></div>
    <div className="grid gap-2"><Label htmlFor="member-password">初始密码（至少 12 位）</Label><Input id="member-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" minLength={12} required /><p className="text-xs leading-5 text-muted-foreground">请通过安全的线下渠道交给成员，用于执行 <code>ith5 login</code>。</p></div>
    <Err msg={err} />
    <DialogFooter><Button type="button" variant="outline" onClick={onCancel}>取消</Button><Button disabled={busy}>{busy ? '创建中…' : '创建成员'}</Button></DialogFooter>
  </form></DialogContent></Dialog>
}

function ResetPassword({ member, onDone, onCancel }: { member: Member; onDone: () => void; onCancel: () => void }) {
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await api.resetMemberPassword(member.id, password)
      onDone()
    } catch (error) {
      setErr((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return <Dialog open onOpenChange={(open) => { if (!open) onCancel() }}><DialogContent><form onSubmit={submit} className="grid gap-5">
    <DialogHeader><DialogTitle>重置密码</DialogTitle><DialogDescription>为 {member.email} 设置新密码。已签发令牌仍然有效，如需撤销访问请停用该成员。</DialogDescription></DialogHeader>
    <div className="grid gap-2"><Label htmlFor="reset-member-password">新密码（至少 12 位）</Label><Input id="reset-member-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" minLength={12} required /></div>
    <Err msg={err} />
    <DialogFooter><Button type="button" variant="outline" onClick={onCancel}>取消</Button><Button disabled={busy}>{busy ? '重置中…' : '确认重置'}</Button></DialogFooter>
  </form></DialogContent></Dialog>
}
