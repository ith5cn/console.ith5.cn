import { useState } from 'react'
import { api } from '../api'
import { Empty, Err, fmtTime, useAsync } from '../ui'

export function Assignments() {
  const list = useAsync(() => api.listAssignments())
  const groups = useAsync(() => api.listGroups())
  const bundles = useAsync(() => api.listBundles())
  const members = useAsync(() => api.listMembers())
  const [adding, setAdding] = useState(false)

  async function remove(id: string) {
    await api.deleteAssignment(id)
    list.reload()
  }

  return (
    <>
      <h1>授权</h1>
      <p className="sub">把权限组（常规）或单个内容（一次性）授权给全组织、某个人或某个项目</p>
      <div className="row" style={{ marginBottom: 14 }}>
        <button className="btn primary" onClick={() => setAdding(true)}>新增授权</button>
      </div>
      {adding && (
        <Add groups={groups.data?.groups ?? []} bundles={bundles.data?.bundles ?? []}
          members={members.data?.members ?? []}
          onDone={() => { setAdding(false); list.reload() }} onCancel={() => setAdding(false)} />
      )}
      <Err msg={list.err} />
      {!list.data?.assignments?.length ? <Empty>还没有授权</Empty> : (
        <table>
          <thead><tr><th>授权目标</th><th>授予给</th><th>有效期</th><th>创建时间</th><th></th></tr></thead>
          <tbody>
            {list.data.assignments.map((a) => (
              <tr key={a.id}>
                <td>
                  {a.group_name
                    ? <span className="tag group">{a.group_name}</span>
                    : <span className="tag">{a.bundle_name}</span>}
                </td>
                <td>
                  {a.subject_type === 'org' ? '全组织' : (a.subject_name || a.subject_id)}
                  <span className="muted"> · {a.subject_type}</span>
                </td>
                <td>
                  {a.expires_at
                    ? <>
                        {fmtTime(a.expires_at)}
                        {/* 到期不删行以保留审计可追溯性，但列表要标出来 */}
                        {a.expired && <span className="tag danger" style={{ marginLeft: 6 }}>已到期</span>}
                      </>
                    : <span className="muted">永久</span>}
                </td>
                <td className="muted">{fmtTime(a.created_at)}</td>
                <td><button className="btn" onClick={() => remove(a.id)}>撤销</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}

function Add({ groups, bundles, members, onDone, onCancel }: {
  groups: { id: string; name: string }[]
  bundles: { id: string; name: string }[]
  members: { id: string; email: string }[]
  onDone: () => void; onCancel: () => void
}) {
  const [mode, setMode] = useState<'group' | 'bundle'>('group')
  const [targetId, setTargetId] = useState('')
  const [subject, setSubject] = useState<'org' | 'user'>('org')
  const [subjectId, setSubjectId] = useState('')
  const [expires, setExpires] = useState('')
  const [err, setErr] = useState('')

  async function submit() {
    setErr('')
    try {
      await api.createAssignment({
        ...(mode === 'group' ? { group_id: targetId } : { bundle_id: targetId }),
        subject_type: subject,
        ...(subject === 'user' ? { subject_id: subjectId } : {}),
        ...(expires ? { expires_at: new Date(expires).toISOString() } : {}),
      })
      onDone()
    } catch (e) { setErr((e as Error).message) }
  }

  const options = mode === 'group' ? groups : bundles
  return (
    <div className="panel">
      <label>授权目标</label>
      <div className="tabs">
        <button className={mode === 'group' ? 'active' : ''} onClick={() => { setMode('group'); setTargetId('') }}>权限组（常规）</button>
        <button className={mode === 'bundle' ? 'active' : ''} onClick={() => { setMode('bundle'); setTargetId('') }}>单个内容（一次性）</button>
      </div>
      <select value={targetId} onChange={(e) => setTargetId(e.target.value)}>
        <option value="">请选择…</option>
        {options.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
      </select>

      <label>授予给</label>
      <select value={subject} onChange={(e) => setSubject(e.target.value as 'org' | 'user')}>
        <option value="org">全组织</option>
        <option value="user">指定成员</option>
      </select>
      {subject === 'user' && (
        <select value={subjectId} onChange={(e) => setSubjectId(e.target.value)} style={{ marginTop: 8 }}>
          <option value="">请选择成员…</option>
          {members.map((m) => <option key={m.id} value={m.id}>{m.email}</option>)}
        </select>
      )}

      <label>到期时间（留空为永久；外包、实习、跨组支援建议设置）</label>
      <input type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
      <Err msg={err} />
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn primary" onClick={submit} disabled={!targetId}>授权</button>
        <button className="btn" onClick={onCancel}>取消</button>
      </div>
    </div>
  )
}
