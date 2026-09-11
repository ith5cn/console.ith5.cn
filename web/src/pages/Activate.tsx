import { useEffect, useState } from 'react'
import { LaptopMinimalCheck, ShieldAlert } from 'lucide-react'
import { api, type Me } from '@/api'
import { AuthShell } from '@/components/AuthShell'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Login } from '@/pages/Login'
import { Err } from '@/ui'

type Peek = Awaited<ReturnType<typeof api.devicePeek>>

// 设备授权流的浏览器一环：登录后输入 CLI 显示的验证码，看清楚要批准什么，再批准。
export function Activate({ me, onLogin }: { me: Me | null; onLogin: (me: Me) => void }) {
  const [code, setCode] = useState(new URLSearchParams(location.search).get('user_code') ?? '')
  const [peek, setPeek] = useState<Peek | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  useEffect(() => {
    if (!me || code.replace(/[-\s]/g, '').length !== 8) { setPeek(null); return }
    let alive = true
    api.devicePeek(code).then((p) => { if (alive) { setPeek(p); setErr('') } }, (e) => { if (alive) { setPeek(null); setErr((e as Error).message) } })
    return () => { alive = false }
  }, [me, code])

  if (!me) return <Login onDone={onLogin} />

  async function approve(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await api.deviceActivate(code)
      setDone(true)
    } catch (error) {
      setErr((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const blocked = peek?.enrollment && !peek.enrollment.allowed

  return (
    <AuthShell title="把这台设备接入团队。">
      <form className="w-full max-w-sm" onSubmit={approve}>
        <h2 className="text-2xl font-semibold tracking-tight">批准设备</h2>
        <p className="mt-2 text-sm text-muted-foreground">以 {me.account.email}（{me.membership.org_name}）的身份批准。</p>
        {done ? (
          <div className="mt-8 rounded-md border bg-muted p-4 text-sm"><LaptopMinimalCheck className="mb-2 size-5 text-primary" />已批准。回到终端，同步会自动继续。</div>
        ) : (
          <>
            <div className="mt-7 space-y-2"><Label htmlFor="user-code">验证码</Label><Input id="user-code" value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="WXYZ-2345" autoFocus /></div>
            {peek && (
              <div className="mt-4 rounded-md border p-3 text-sm">
                <div><span className="text-muted-foreground">设备：</span>{peek.hostname || '未知'}{peek.os ? ` · ${peek.os}` : ''}</div>
                {peek.enrollment && (
                  <div className="mt-1"><span className="text-muted-foreground">接入码限定项目：</span>{peek.enrollment.project_ids.length} 个
                    {blocked && <div className="mt-2 flex items-start gap-2 text-destructive"><ShieldAlert className="mt-0.5 size-4 shrink-0" />你不是这些项目的成员，无法批准。请联系管理员先把你加入项目。</div>}
                  </div>
                )}
              </div>
            )}
            <Err msg={err} />
            <Button className="mt-6 w-full" size="lg" disabled={busy || !peek || !!blocked}>{busy ? '批准中…' : '批准这台设备'}</Button>
          </>
        )}
      </form>
    </AuthShell>
  )
}
