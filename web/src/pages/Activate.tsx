import { useState } from 'react'
import { CheckCircle2 } from 'lucide-react'
import { api } from '../api'
import { AuthShell } from '@/components/AuthShell'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Err } from '../ui'

export function Activate() {
  const params = new URLSearchParams(location.search)
  const [code, setCode] = useState(params.get('code') ?? '')
  const [org, setOrg] = useState('demo'); const [email, setEmail] = useState(''); const [pw, setPw] = useState(''); const [err, setErr] = useState(''); const [done, setDone] = useState(false); const [busy, setBusy] = useState(false)
  async function submit(e: React.FormEvent) { e.preventDefault(); setErr(''); setBusy(true); try { await api.activate(code, org, email, pw); setDone(true) } catch (error) { setErr((error as Error).message) } finally { setBusy(false) } }
  return <AuthShell title="把这台设备，安全地接入团队能力。">{done ? <div className="w-full max-w-sm text-center"><span className="mx-auto grid size-14 place-items-center rounded-full bg-emerald-50 text-success"><CheckCircle2 className="size-7" /></span><h2 className="mt-5 text-2xl font-semibold">设备已批准</h2><p className="mt-2 text-muted-foreground">回到终端，登录会自动完成。</p></div> : <form className="w-full max-w-sm" onSubmit={submit}><h2 className="text-2xl font-semibold">绑定设备</h2><p className="mt-2 text-sm text-muted-foreground">输入终端里显示的验证码并完成身份验证</p><div className="mt-7 space-y-5"><div className="space-y-2"><Label htmlFor="activate-code">验证码</Label><Input id="activate-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="XXXX-XXXX" className="font-mono uppercase tracking-[.18em]" required autoFocus /></div><div className="space-y-2"><Label htmlFor="activate-org">组织</Label><Input id="activate-org" value={org} onChange={(e) => setOrg(e.target.value)} required /></div><div className="space-y-2"><Label htmlFor="activate-email">邮箱</Label><Input id="activate-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" required /></div><div className="space-y-2"><Label htmlFor="activate-password">密码</Label><Input id="activate-password" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" required /></div></div><Err msg={err} /><Button className="mt-6 w-full" size="lg" disabled={busy}>{busy ? '批准中…' : '批准此设备'}</Button></form>}</AuthShell>
}
