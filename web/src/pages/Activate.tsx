import { useState } from 'react'
import { api } from '../api'
import { Err } from '../ui'

// 设备激活页：CLI 登录流程的 Web 一环。
// 不需要先登录后台——用户在这里直接用账号密码 + CLI 显示的验证码完成绑定。
export function Activate() {
  const params = new URLSearchParams(location.search)
  const [code, setCode] = useState(params.get('code') ?? '')
  const [org, setOrg] = useState('demo')
  const [email, setEmail] = useState('')
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  const [done, setDone] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setErr('')
    try { await api.activate(code, org, email, pw); setDone(true) }
    catch (e) { setErr((e as Error).message) }
  }

  if (done) {
    return (
      <div className="login panel">
        <h1>已批准</h1>
        <p className="sub">回到终端，登录会自动完成。</p>
      </div>
    )
  }
  return (
    <form className="login panel" onSubmit={submit}>
      <h1>绑定设备</h1>
      <p className="sub">输入终端里显示的验证码</p>
      <label>验证码</label>
      <input value={code} onChange={(e) => setCode(e.target.value)}
        placeholder="XXXX-XXXX" style={{ letterSpacing: '.1em' }} />
      <label>组织</label>
      <input value={org} onChange={(e) => setOrg(e.target.value)} />
      <label>邮箱</label>
      <input value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" />
      <label>密码</label>
      <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" />
      <Err msg={err} />
      <div style={{ marginTop: 14 }}>
        <button className="btn primary">批准</button>
      </div>
    </form>
  )
}
