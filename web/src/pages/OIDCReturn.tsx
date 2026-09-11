import { useEffect, useState } from 'react'
import { api, type Me } from '@/api'
import { AuthShell } from '@/components/AuthShell'
import { Err } from '@/ui'

// OIDC 回调把会话令牌放在 URL fragment 里送回来；这里取出、验证、写入本地后回到控制台。
export function OIDCReturn({ onDone }: { onDone: (me: Me) => void }) {
  const [err, setErr] = useState('')
  useEffect(() => {
    const token = new URLSearchParams(location.hash.replace(/^#/, '')).get('access_token')
    if (!token) {
      setErr('缺少令牌，请重新登录')
      return
    }
    api.adoptToken(token).then((me) => {
      history.replaceState(null, '', '/#/overview')
      onDone(me)
    }, (e) => setErr((e as Error).message))
  }, [onDone])
  return <AuthShell><div className="w-full max-w-sm"><h2 className="text-2xl font-semibold tracking-tight">正在完成登录…</h2><Err msg={err} />{err && <a className="mt-4 inline-block text-sm underline" href="/">返回登录页</a>}</div></AuthShell>
}
