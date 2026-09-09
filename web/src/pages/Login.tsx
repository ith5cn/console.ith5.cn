import { useState } from 'react'
import { ArrowRight } from 'lucide-react'
import { api, type UserInfo } from '@/api'
import { AuthShell } from '@/components/AuthShell'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Err } from '@/ui'
export function Login({ onDone }: { onDone: (u: UserInfo) => void }) {
  const [org,setOrg]=useState('demo'); const [email,setEmail]=useState(''); const [pw,setPw]=useState(''); const [err,setErr]=useState(''); const [busy,setBusy]=useState(false)
  async function submit(e:React.FormEvent){e.preventDefault();setBusy(true);setErr('');try{onDone(await api.login(org,email,pw))}catch(error){setErr((error as Error).message)}finally{setBusy(false)}}
  return <AuthShell><form className="w-full max-w-sm" onSubmit={submit}><h2 className="text-2xl font-semibold tracking-tight">登录控制台</h2><p className="mt-2 text-sm text-muted-foreground">使用组织管理员账号继续</p><div className="mt-7 space-y-5"><div className="space-y-2"><Label htmlFor="login-org">组织</Label><Input id="login-org" value={org} onChange={(e)=>setOrg(e.target.value)} autoComplete="organization" required /></div><div className="space-y-2"><Label htmlFor="login-email">邮箱</Label><Input id="login-email" type="email" value={email} onChange={(e)=>setEmail(e.target.value)} autoComplete="username" required /></div><div className="space-y-2"><Label htmlFor="login-password">密码</Label><Input id="login-password" type="password" value={pw} onChange={(e)=>setPw(e.target.value)} autoComplete="current-password" required /></div></div><Err msg={err}/><Button className="mt-6 w-full" size="lg" disabled={busy}>{busy?'登录中…':<>进入控制台 <ArrowRight/></>}</Button><p className="mt-6 text-center text-xs text-muted-foreground">ITH5 Community Edition · AGPL-3.0</p></form></AuthShell>
}
