import { useState } from 'react'
import { api, type Bundle, type FileItem, type VersionInfo } from '../api'
import { Empty, Err, fmtTime, useAsync } from '../ui'

export function Bundles() {
  const { data, err, loading, reload } = useAsync(() => api.listBundles())
  const [editing, setEditing] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)

  if (editing) return <Editor id={editing} onBack={() => { setEditing(null); reload() }} />

  return (
    <>
      <h1>内容</h1>
      <p className="sub">
        skill 与 command 落盘为 ~/.claude/skills/&lt;名称&gt;/SKILL.md，两者只差谁能触发；
        agent 落盘为 ~/.claude/agents/&lt;名称&gt;.md，是可被派发的 subagent
      </p>
      <div className="row" style={{ marginBottom: 14 }}>
        <button className="btn primary" onClick={() => setCreating(true)}>新建</button>
        <div className="spacer" />
        <button className="btn" onClick={reload}>刷新</button>
      </div>
      {creating && <Create onDone={(id) => { setCreating(false); setEditing(id) }} onCancel={() => setCreating(false)} />}
      <Err msg={err} />
      {loading ? <Empty>加载中…</Empty> : !data?.bundles?.length ? <Empty>还没有内容，先新建一个</Empty> : (
        <table>
          <thead>
            <tr><th>名称</th><th>类型</th><th>版本</th><th>所属权限组</th><th>更新时间</th><th></th></tr>
          </thead>
          <tbody>
            {data.bundles.map((b: Bundle) => (
              <tr key={b.id}>
                <td>
                  <strong>{b.name}</strong>{b.archived && <span className="tag warn" style={{ marginLeft: 6 }}>已归档</span>}
                  <div className="muted">{b.description}</div>
                </td>
                <td><span className="tag">{b.kind}</span></td>
                <td>{b.latest_version > 0 ? `v${b.latest_version}` : <span className="muted">未发布</span>}</td>
                <td>{b.groups?.length ? b.groups.map((g) => <span key={g} className="tag group">{g}</span>) : <span className="muted">—</span>}</td>
                <td className="muted">{fmtTime(b.updated_at)}</td>
                <td><button className="btn" onClick={() => setEditing(b.id)}>编辑</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}

function Create({ onDone, onCancel }: { onDone: (id: string) => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [kind, setKind] = useState('skill')
  const [desc, setDesc] = useState('')
  const [content, setContent] = useState('')
  const [err, setErr] = useState('')

  // 留空时服务端会按类型种一份模板；这里预览它，让人知道会得到什么。
  // agent 的模板必须带 name 且等于 bundle 名——发布时会硬校验，
  // 这里就把它显示出来，省得管理员改了名字忘了同步。
  const placeholder =
    kind === 'command'
      ? '---\ndescription: （留空则用下面这段模板）\ndisable-model-invocation: true\n---\n\n在这里写下这个命令要执行的步骤。'
      : kind === 'agent'
        ? `---\nname: ${name || '（这里必须等于上面的名称）'}\ndescription: （留空则用下面这段模板）\n---\n\n在这里写下这个 subagent 的职责、工作流程与交付格式。`
        : '---\ndescription: （留空则用下面这段模板）\n---\n\n在这里写下 Claude 应当遵循的指令。'

  async function submit() {
    setErr('')
    try {
      const r = await api.createBundle(name, kind, desc, content)
      onDone(r.id)
    } catch (e) { setErr((e as Error).message) }
  }

  return (
    <div className="panel">
      <label>{kind === 'agent' ? '名称（小写、连字符；派发时用的就是这个名字）' : '名称（小写、连字符；会成为 /名称 这个命令）'}</label>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="corp-api-review" />

      <label>类型</label>
      <select value={kind} onChange={(e) => setKind(e.target.value)}>
        <option value="skill">skill —— Claude 判断相关时可自动调用</option>
        <option value="command">command —— 只有用户敲 /名称 才触发</option>
        <option value="agent">agent —— 可被派发的 subagent</option>
      </select>
      {kind === 'agent' ? (
        <p className="muted" style={{ marginTop: 4 }}>
          落盘为 <code>~/.claude/agents/&lt;名称&gt;.md</code>，<b>只能有这一个文件</b>，不能带支持文件。
          正文的 <code>name:</code> 必须与上面的名称完全一致——派发时用的是这个名称，
          对不上的话 agent 装得上但永远派发不到，而且本地看不出任何异常。发布时会拒绝。
        </p>
      ) : (
        <p className="muted" style={{ marginTop: 4 }}>
          skill 与 command 落盘位置相同，只决定 SKILL.md 里的 frontmatter。
          选 command 时正文必须带 <code>disable-model-invocation: true</code>，
          发布时会校验，不一致会被拒绝。
        </p>
      )}

      <label>说明（会写进 frontmatter 的 description，Claude 据此判断何时使用）</label>
      <input value={desc} onChange={(e) => setDesc(e.target.value)} />

      <label>正文（可留空，创建后在编辑器里继续写）</label>
      <textarea className="mono" rows={10} value={content} placeholder={placeholder}
        onChange={(e) => setContent(e.target.value)} />

      <Err msg={err} />
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn primary" onClick={submit} disabled={!name}>创建并编辑</button>
        <button className="btn" onClick={onCancel}>取消</button>
      </div>
    </div>
  )
}

function Editor({ id, onBack }: { id: string; onBack: () => void }) {
  const { data, err, reload } = useAsync(() => api.getBundle(id), [id])
  const vers = useAsync(() => api.listVersions(id), [id])
  const [files, setFiles] = useState<FileItem[] | null>(null)
  const [changelog, setChangelog] = useState('')
  const [msg, setMsg] = useState('')
  const [oops, setOops] = useState('')

  const current = files ?? data?.draft_files ?? []
  const setFile = (i: number, patch: Partial<FileItem>) =>
    setFiles(current.map((f, j) => (j === i ? { ...f, ...patch } : f)))

  async function save() {
    setOops(''); setMsg('')
    try { await api.saveDraft(id, current); setMsg('草稿已保存'); reload() }
    catch (e) { setOops((e as Error).message) }
  }
  async function publish() {
    setOops(''); setMsg('')
    try {
      const r = await api.publish(id, changelog)
      setMsg(`已发布 v${r.version}`); setChangelog(''); setFiles(null); reload(); vers.reload()
    } catch (e) { setOops((e as Error).message) }
  }
  async function rollback(v: number) {
    setOops(''); setMsg('')
    try {
      const r = await api.rollback(id, v)
      setMsg(`已回滚：用 v${v} 的内容发布了 v${r.version}`)
      setFiles(null); reload(); vers.reload()
    } catch (e) { setOops((e as Error).message) }
  }

  return (
    <>
      <button className="btn" onClick={onBack}>← 返回</button>
      <h1 style={{ marginTop: 12 }}>{data?.name ?? '…'}</h1>
      <p className="sub">
        当前发布版本 {data?.latest_version ? `v${data.latest_version}` : '无'} ·
        草稿改动只有点「发布」才会下发给员工
      </p>
      <Err msg={err} /><Err msg={oops} />
      {msg && <div className="ok">{msg}</div>}

      <div className="panel">
        {current.map((f, i) => (
          <div key={i} style={{ marginBottom: 14 }}>
            <div className="row">
              <input value={f.path} onChange={(e) => setFile(i, { path: e.target.value })}
                style={{ maxWidth: 320 }} />
              <button className="btn" onClick={() => setFiles(current.filter((_, j) => j !== i))}>删除</button>
            </div>
            <textarea className="mono" rows={f.path === 'SKILL.md' ? 12 : 6} value={f.content}
              onChange={(e) => setFile(i, { content: e.target.value })} style={{ marginTop: 6 }} />
          </div>
        ))}
        <button className="btn" onClick={() => setFiles([...current, { path: '', content: '' }])}>+ 添加文件</button>
        <p className="muted" style={{ marginTop: 10 }}>必须包含 SKILL.md。路径为相对路径，不得包含 .. 或绝对路径。</p>
        <div className="row" style={{ marginTop: 12 }}>
          <button className="btn" onClick={save}>保存草稿</button>
          <input placeholder="本次变更说明" value={changelog}
            onChange={(e) => setChangelog(e.target.value)} style={{ maxWidth: 300 }} />
          <button className="btn primary" onClick={publish}>发布新版本</button>
        </div>
      </div>

      <h1 style={{ fontSize: 16 }}>版本历史</h1>
      {!vers.data?.versions?.length ? <Empty>尚未发布过</Empty> : (
        <table>
          <thead><tr><th>版本</th><th>说明</th><th>发布者</th><th>时间</th><th></th></tr></thead>
          <tbody>
            {vers.data.versions.map((v: VersionInfo) => (
              <tr key={v.version}>
                <td>
                  v{v.version}
                  {/* 回滚在审计里必须可见——changelog 是自由文本，承担不了这个职责 */}
                  {v.rollback_of_version ? <span className="tag warn" style={{ marginLeft: 6 }}>回滚自 v{v.rollback_of_version}</span> : null}
                </td>
                <td>{v.changelog || <span className="muted">—</span>}</td>
                <td className="muted">{v.published_by}</td>
                <td className="muted">{fmtTime(v.published_at)}</td>
                <td>
                  {v.version !== data?.latest_version &&
                    <button className="btn" onClick={() => rollback(v.version)}>回滚到此版</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}
