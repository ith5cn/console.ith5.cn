import { useState } from 'react'
import { api, getUser, type Member } from '../api'
import { Empty, Err, fmtTime, useAsync } from '../ui'

export function Members() {
  const { data, err, reload } = useAsync(() => api.listMembers())
  const [creating, setCreating] = useState(false)
  const [resetting, setResetting] = useState<Member | null>(null)
  const isOwner = getUser()?.role === 'owner'

  async function toggle(id: string, status: string) {
    await api.setMemberStatus(id, status === 'active' ? 'suspended' : 'active')
    reload()
  }

  return (
    <>
      <h1>成员</h1>
      <p className="sub">
        停用后该成员下次同步即停止获取任何更新，客户端会清理本机已分发的内容。
        <strong>但这依赖对方主动运行同步——切断续期，不保证擦除。</strong>
      </p>
      <div className="row" style={{ marginBottom: 14 }}>
        <button className="btn primary" onClick={() => { setResetting(null); setCreating(true) }}>新增成员</button>
      </div>
      {creating && (
        <Create isOwner={isOwner}
          onDone={() => { setCreating(false); reload() }}
          onCancel={() => setCreating(false)} />
      )}
      {resetting && (
        <ResetPassword member={resetting} onDone={() => setResetting(null)} />
      )}
      <Err msg={err} />
      {!data?.members?.length ? <Empty>没有成员</Empty> : (
        <table>
          <thead><tr><th>邮箱</th><th>角色</th><th>状态</th><th>设备</th><th>最后活跃</th><th></th></tr></thead>
          <tbody>
            {data.members.map((m) => (
              <tr key={m.id}>
                <td><strong>{m.email}</strong>{m.name && <div className="muted">{m.name}</div>}</td>
                <td><span className="tag">{m.role}</span></td>
                <td>
                  {m.status === 'active'
                    ? <span className="tag ok">正常</span>
                    : <span className="tag danger">已停用</span>}
                </td>
                <td>{m.machines}</td>
                <td className="muted">{fmtTime(m.last_seen_at)}</td>
                <td>
                  <div className="row">
                    <button className="btn" onClick={() => toggle(m.id, m.status)}>
                      {m.status === 'active' ? '停用' : '恢复'}
                    </button>
                    {/* admin 改不了管理员的密码，那是 owner 的事 */}
                    {(isOwner || m.role === 'member') && (
                      <button className="btn" onClick={() => { setCreating(false); setResetting(m) }}>重置密码</button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}

// Create 建号并当场设初始密码。
//
// 系统里没有邮件设施，所以没有邀请链接这回事：管理员把初始密码线下交给员工，
// 员工用它跑 ith5 login。
function Create({ isOwner, onDone, onCancel }: { isOwner: boolean; onDone: () => void; onCancel: () => void }) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState('member')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true)
    try { await api.createMember(email, name, role, password); onDone() }
    catch (e) { setErr((e as Error).message) }
    finally { setBusy(false) }
  }

  return (
    <div className="panel">
      <label>邮箱（登录账号）</label>
      <input value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />
      <label>姓名</label>
      <input value={name} onChange={(e) => setName(e.target.value)} />
      <label>角色</label>
      <select value={role} onChange={(e) => setRole(e.target.value)}>
        <option value="member">member · 只用客户端</option>
        {/* 只有 owner 能扩大管理面，admin 不能自己造出更多 admin */}
        {isOwner && <option value="admin">admin · 可进管理后台</option>}
      </select>
      <label>初始密码（至少 12 位）</label>
      <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
      <p className="sub">
        建号后把初始密码线下交给本人，他用它跑 <code>ith5 login</code>。
        <strong>系统不发邮件，这串密码只有你这里有。</strong>
      </p>
      <Err msg={err} />
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn primary" disabled={busy} onClick={submit}>创建</button>
        <button className="btn" onClick={onCancel}>取消</button>
      </div>
    </div>
  )
}

function ResetPassword({ member, onDone }: { member: Member; onDone: () => void }) {
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true)
    try { await api.resetMemberPassword(member.id, password); onDone() }
    catch (e) { setErr((e as Error).message) }
    finally { setBusy(false) }
  }

  return (
    <div className="panel">
      <h1 style={{ fontSize: 16 }}>重置「{member.email}」的密码</h1>
      <p className="sub">
        只改密码，<strong>不会踢他下线</strong>——已签发的令牌照常有效，那是停用要干的事。
      </p>
      <label>新密码（至少 12 位）</label>
      <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
      <Err msg={err} />
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn primary" disabled={busy} onClick={submit}>重置</button>
        <button className="btn" onClick={onDone}>取消</button>
      </div>
    </div>
  )
}
