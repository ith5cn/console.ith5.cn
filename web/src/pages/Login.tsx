import { useState } from 'react'
import { ArrowRight, Building2 } from 'lucide-react'
import { api, type Me, type Membership } from '@/api'
import { AuthShell } from '@/components/AuthShell'
import { Button, buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Err } from '@/ui'

// 登录分两步：先用邮箱密码证明「你是这个账号」，再选一个组织换会话令牌。
// 只属于一个组织时直接进入，不让用户多点一次。
export function Login({ onDone }: { onDone: (me: Me) => void }) {
  const [email, setEmail] = useState('')
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [choice, setChoice] = useState<{ token: string; orgs: Membership[] } | null>(null)
  // 新建组织：没有任何组织的账号从这里开始；已有组织的也可以再建一个
  const [creating, setCreating] = useState<{ token: string } | null>(null)
  const [orgName, setOrgName] = useState('')
  const [orgSlug, setOrgSlug] = useState('')

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      const r = await api.login(email, pw)
      if (r.organizations.length === 0) {
        setCreating({ token: r.login_token })
      } else if (r.organizations.length === 1) {
        onDone(await api.session(r.login_token, r.organizations[0].user_id))
      } else {
        setChoice({ token: r.login_token, orgs: r.organizations })
      }
    } catch (error) {
      setErr((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function pick(m: Membership) {
    if (!choice) return
    setBusy(true)
    setErr('')
    try {
      onDone(await api.session(choice.token, m.user_id))
    } catch (error) {
      setErr((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function createOrg(e: React.FormEvent) {
    e.preventDefault()
    if (!creating) return
    setBusy(true)
    setErr('')
    try {
      const r = await api.createOrganization(creating.token, orgName, orgSlug)
      onDone(await api.session(creating.token, r.membership.user_id))
    } catch (error) {
      setErr((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const oidcSlug = new URLSearchParams(location.search).get('org')

  if (creating) {
    return (
      <AuthShell>
        <form className="w-full max-w-sm" onSubmit={createOrg}>
          <h2 className="text-2xl font-semibold tracking-tight">新建组织</h2>
          <p className="mt-2 text-sm text-muted-foreground">{choice ? '再建一个组织，你将成为它的 owner。' : '这个账号还不属于任何组织。建一个，你就是它的 owner。'}</p>
          <div className="mt-7 space-y-5">
            <div className="space-y-2"><Label htmlFor="org-name">组织名称</Label><Input id="org-name" value={orgName} onChange={(e) => setOrgName(e.target.value)} required /></div>
            <div className="space-y-2"><Label htmlFor="org-slug">slug</Label><Input id="org-slug" value={orgSlug} onChange={(e) => setOrgSlug(e.target.value)} placeholder="acme" pattern="[a-z0-9]([a-z0-9-]*[a-z0-9])?" required /><p className="text-xs text-muted-foreground">小写字母、数字、连字符；用于登录时选组织与单点登录链接，建好后不能改。</p></div>
          </div>
          <Err msg={err} />
          <Button className="mt-6 w-full" size="lg" disabled={busy}>{busy ? '创建中…' : '创建并进入'}</Button>
          {choice && <Button type="button" variant="ghost" className="mt-2 w-full" onClick={() => setCreating(null)}>返回选择组织</Button>}
        </form>
      </AuthShell>
    )
  }

  return (
    <AuthShell>
      {choice ? (
        <div className="w-full max-w-sm">
          <h2 className="text-2xl font-semibold tracking-tight">选择组织</h2>
          <p className="mt-2 text-sm text-muted-foreground">这个账号属于多个组织，令牌是组织内的。</p>
          <div className="mt-6 grid gap-2">
            {choice.orgs.map((m) => (
              <button key={m.user_id} type="button" disabled={busy} onClick={() => pick(m)}
                className="focus-ring flex items-center gap-3 rounded-md border p-3 text-left hover:bg-muted">
                <Building2 className="size-4 text-muted-foreground" />
                <span className="min-w-0 flex-1"><span className="block font-medium">{m.org_name}</span><span className="block text-xs text-muted-foreground">{m.org_slug} · {m.role}</span></span>
                <ArrowRight className="size-4 text-muted-foreground" />
              </button>
            ))}
          </div>
          <Err msg={err} />
          <Button type="button" variant="ghost" className="mt-4 w-full" onClick={() => setCreating({ token: choice.token })}>新建组织</Button>
        </div>
      ) : (
        <form className="w-full max-w-sm" onSubmit={submit}>
          <h2 className="text-2xl font-semibold tracking-tight">登录控制台</h2>
          <p className="mt-2 text-sm text-muted-foreground">使用组织账号继续</p>
          <div className="mt-7 space-y-5">
            <div className="space-y-2"><Label htmlFor="login-email">邮箱</Label><Input id="login-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" required /></div>
            <div className="space-y-2"><Label htmlFor="login-password">密码</Label><Input id="login-password" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" required /></div>
          </div>
          <Err msg={err} />
          <Button className="mt-6 w-full" size="lg" disabled={busy}>{busy ? '登录中…' : <>进入控制台 <ArrowRight /></>}</Button>
          {oidcSlug && (
            <a className={cn(buttonVariants({ variant: 'outline', size: 'lg' }), 'mt-3 w-full')} href={`/v1/auth/oidc/authorize?org=${encodeURIComponent(oidcSlug)}`}>使用企业单点登录</a>
          )}
          <p className="mt-6 text-center text-xs text-muted-foreground">ITH5 · AGPL-3.0</p>
        </form>
      )}
    </AuthShell>
  )
}
