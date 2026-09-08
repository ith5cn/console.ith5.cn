import { useState } from 'react'
import { api, type Group } from '../api'
import { Empty, Err, useAsync } from '../ui'

export function Groups() {
  const groups = useAsync(() => api.listGroups())
  const bundles = useAsync(() => api.listBundles())
  const [editing, setEditing] = useState<Group | null>(null)
  const [creating, setCreating] = useState(false)

  return (
    <>
      <h1>权限组</h1>
      <p className="sub">
        权限组装配 skill / command，再把组授权给人。
        <strong>往组里加内容后，所有被授权者下次同步即自动拿到，不需要改任何一条授权。</strong>
      </p>
      <div className="row" style={{ marginBottom: 14 }}>
        <button className="btn primary" onClick={() => setCreating(true)}>新建权限组</button>
      </div>
      {creating && <Create onDone={() => { setCreating(false); groups.reload() }} onCancel={() => setCreating(false)} />}
      <Err msg={groups.err} />
      {!groups.data?.groups?.length ? <Empty>还没有权限组</Empty> : (
        <table>
          <thead><tr><th>权限组</th><th>包含内容</th><th>已授权给</th><th></th></tr></thead>
          <tbody>
            {groups.data.groups.map((g) => (
              <tr key={g.id}>
                <td>
                  <strong>{g.name}</strong>
                  {g.archived && <span className="tag warn" style={{ marginLeft: 6 }}>已归档</span>}
                  <div className="muted">{g.key}{g.description ? ` · ${g.description}` : ''}</div>
                </td>
                <td>
                  {g.bundle_names.length
                    ? g.bundle_names.map((n) => <span key={n} className="tag">{n}</span>)
                    : <span className="muted">空组</span>}
                </td>
                <td>{g.assigned_to} 条授权</td>
                <td><button className="btn" onClick={() => setEditing(g)}>装配</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {editing && bundles.data && (
        <Assemble group={editing} all={bundles.data.bundles}
          onDone={() => { setEditing(null); groups.reload() }} />
      )}
    </>
  )
}

function Create({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [key, setKey] = useState('')
  const [name, setName] = useState('')
  const [desc, setDesc] = useState('')
  const [err, setErr] = useState('')
  async function submit() {
    try { await api.createGroup(key, name, desc); onDone() }
    catch (e) { setErr((e as Error).message) }
  }
  return (
    <div className="panel">
      <label>标识（英文，如 backend-pack）</label>
      <input value={key} onChange={(e) => setKey(e.target.value)} />
      <label>显示名（如 后端工具包）</label>
      <input value={name} onChange={(e) => setName(e.target.value)} />
      <label>说明</label>
      <input value={desc} onChange={(e) => setDesc(e.target.value)} />
      <Err msg={err} />
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn primary" onClick={submit}>创建</button>
        <button className="btn" onClick={onCancel}>取消</button>
      </div>
    </div>
  )
}

function Assemble({ group, all, onDone }: { group: Group; all: { id: string; name: string; kind: string }[]; onDone: () => void }) {
  const [sel, setSel] = useState<string[]>(group.bundle_ids)
  const [err, setErr] = useState('')
  const toggle = (id: string) =>
    setSel(sel.includes(id) ? sel.filter((x) => x !== id) : [...sel, id])

  async function save() {
    try { await api.setGroupBundles(group.id, sel); onDone() }
    catch (e) { setErr((e as Error).message) }
  }
  return (
    <div className="panel" style={{ marginTop: 16 }}>
      <h1 style={{ fontSize: 16 }}>装配「{group.name}」</h1>
      <p className="sub">一个内容可以同时属于多个权限组，公用内容不必复制多份。</p>
      <div className="row wrap">
        {all.map((b) => (
          <label key={b.id} className="row" style={{ width: 'auto', gap: 6, margin: '4px 12px 4px 0' }}>
            <input type="checkbox" style={{ width: 'auto' }}
              checked={sel.includes(b.id)} onChange={() => toggle(b.id)} />
            {b.name} <span className="tag">{b.kind}</span>
          </label>
        ))}
      </div>
      <Err msg={err} />
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn primary" onClick={save}>保存（{sel.length} 项）</button>
        <button className="btn" onClick={onDone}>取消</button>
      </div>
    </div>
  )
}
