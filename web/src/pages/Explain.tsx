import { useState } from 'react'
import { api } from '../api'
import { Empty, Err, Via, useAsync } from '../ui'

// 授权解释器回答管理员最常问的两个问题：
//   「他为什么能拿到这个」→ 看来源标签
//   「我授权了他为什么没有」→ 不在列表里，配合内容的归档/发布状态判断
export function Explain() {
  const members = useAsync(() => api.listMembers())
  const [uid, setUid] = useState('')
  const result = useAsync(
    () => (uid ? api.explain(uid) : Promise.resolve(null)),
    [uid],
  )

  return (
    <>
      <h1>授权解释器</h1>
      <p className="sub">选一个成员，看他能拿到什么，以及凭什么拿到</p>
      <div className="panel">
        <label>成员</label>
        <select value={uid} onChange={(e) => setUid(e.target.value)}>
          <option value="">请选择…</option>
          {members.data?.members.map((m) => (
            <option key={m.id} value={m.id}>{m.email}</option>
          ))}
        </select>
      </div>

      <Err msg={result.err} />
      {!uid ? null : result.data?.suspended ? (
        <Empty>该成员已停用，同步时会拿到空清单并清理本机内容</Empty>
      ) : !result.data?.grants?.length ? (
        <Empty>该成员目前拿不到任何内容</Empty>
      ) : (
        <table>
          <thead><tr><th>内容</th><th>类型</th><th>版本</th><th>凭什么拿到</th></tr></thead>
          <tbody>
            {result.data.grants.map((g) => (
              <tr key={g.bundle_id}>
                <td><strong>{g.bundle_name}</strong></td>
                <td><span className="tag">{g.kind}</span></td>
                <td>v{g.version}</td>
                <td><Via via={g.via} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}
