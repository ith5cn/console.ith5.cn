import { api } from '../api'
import { Empty, Err, fmtTime, useAsync } from '../ui'

export function Members() {
  const { data, err, reload } = useAsync(() => api.listMembers())

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
                  <button className="btn" onClick={() => toggle(m.id, m.status)}>
                    {m.status === 'active' ? '停用' : '恢复'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}
