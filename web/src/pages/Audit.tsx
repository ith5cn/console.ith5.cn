import { useState } from 'react'
import { api } from '../api'
import { Empty, Err, downloadCSV, fmtTime, toCSV, useAsync } from '../ui'

type Tab = 'all' | 'exec' | 'blocked' | 'stale'

export function Audit() {
  const [tab, setTab] = useState<Tab>('all')
  const dist = useAsync(() => api.audit(tab === 'blocked' ? 'conflict_skipped' : undefined), [tab])
  const exec = useAsync(() => api.executions(), [tab])
  const stale = useAsync(() => api.staleMachines(), [tab])

  function exportCSV() {
    if (tab === 'stale') {
      downloadCSV('stale-machines.csv', toCSV(stale.data?.machines ?? [], [
        ['email', '成员'], ['hostname', '设备'], ['last_seen_at', '最后活跃'],
        ['days', '已停(天)'], ['user_level', '用户级掉队'],
      ]))
    } else if (tab === 'exec') {
      const rows = (exec.data?.entries ?? []).map((e) => ({
        occurred_at: e.occurred_at, email: e.email, hostname: e.hostname,
        tool_name: e.tool_name ?? e.event_type, repo: e.summary.repo ?? '',
        file_path: e.summary.file_path ?? '', bash_command: e.summary.bash_command ?? '',
        lines_changed: e.summary.lines_changed ?? '',
      }))
      downloadCSV('executions.csv', toCSV(rows, [
        ['occurred_at', '时间'], ['email', '成员'], ['hostname', '设备'],
        ['tool_name', '工具'], ['repo', '仓库'], ['file_path', '文件'],
        ['bash_command', '命令'], ['lines_changed', '改动行数'],
      ]))
    } else {
      downloadCSV(tab === 'blocked' ? 'blocked.csv' : 'distributions.csv',
        toCSV(dist.data?.entries ?? [], [
          ['created_at', '时间'], ['email', '成员'], ['hostname', '设备'],
          ['bundle_name', '内容'], ['version', '版本'], ['action', '动作'], ['detail', '详情'],
        ]))
    }
  }

  return (
    <>
      <h1>审计</h1>
      <p className="sub">谁在什么时候拿到了哪个版本，以及谁掉了队</p>
      <div className="row" style={{ marginBottom: 10 }}>
        <div className="spacer" />
        <button className="btn" onClick={exportCSV}>导出 CSV</button>
      </div>
      <div className="tabs">
        <button className={tab === 'all' ? 'active' : ''} onClick={() => setTab('all')}>分发记录</button>
        <button className={tab === 'exec' ? 'active' : ''} onClick={() => setTab('exec')}>执行记录</button>
        <button className={tab === 'blocked' ? 'active' : ''} onClick={() => setTab('blocked')}>下发受阻</button>
        <button className={tab === 'stale' ? 'active' : ''} onClick={() => setTab('stale')}>长期未同步</button>
      </div>

      {tab === 'blocked' && (
        <p className="sub">
          目标路径已被员工自有的同名内容占用，因此跳过、且未做任何改动。
          没有这张表，「从未分配」和「一直装不上」在审计里长得一模一样。
        </p>
      )}
      {tab === 'stale' && (
        <p className="sub">
          <strong>用户级</strong>表示该成员所有设备都掉队，可能已离职未处理；
          仅部分设备掉队通常只是换机或闲置。
        </p>
      )}

      {tab === 'exec' && (
        <p className="sub">
          只记录元数据：谁、何时、用什么工具、动了哪个仓库的哪个文件。
          <strong>文件内容、diff、完整命令行、提示词一律不上报。</strong>
        </p>
      )}

      {tab === 'exec' ? (
        <>
          <Err msg={exec.err} />
          {!exec.data?.entries?.length ? <Empty>暂无执行记录</Empty> : (
            <table>
              <thead><tr><th>时间</th><th>成员</th><th>工具</th><th>动作</th></tr></thead>
              <tbody>
                {exec.data.entries.map((e, i) => (
                  <tr key={i}>
                    <td className="muted">{fmtTime(e.occurred_at)}</td>
                    <td>{e.email}<div className="muted">{e.hostname || '—'}</div></td>
                    <td><span className="tag">{e.tool_name || e.event_type}</span></td>
                    <td>
                      {e.summary.file_path ? (
                        <>
                          改了 <code>{e.summary.file_path}</code>
                          {e.summary.lines_changed ? <span className="muted"> · {e.summary.lines_changed} 行</span> : null}
                        </>
                      ) : e.summary.bash_command ? (
                        <>执行 <code>{e.summary.bash_command}</code></>
                      ) : <span className="muted">—</span>}
                      {e.summary.repo && <div className="muted">{e.summary.repo}</div>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      ) : tab === 'stale' ? (
        <>
          <Err msg={stale.err} />
          {!stale.data?.machines?.length ? <Empty>没有掉队的设备</Empty> : (
            <table>
              <thead><tr><th>成员</th><th>设备</th><th>最后活跃</th><th>已停</th><th>范围</th></tr></thead>
              <tbody>
                {stale.data.machines.map((m, i) => (
                  <tr key={i}>
                    <td>{m.email}</td>
                    <td className="muted">{m.hostname || '—'}</td>
                    <td className="muted">{fmtTime(m.last_seen_at)}</td>
                    <td>{m.days} 天</td>
                    <td>{m.user_level
                      ? <span className="tag danger">用户级</span>
                      : <span className="tag">设备级</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      ) : (
        <>
          <Err msg={dist.err} />
          {!dist.data?.entries?.length ? <Empty>暂无记录</Empty> : (
            <table>
              <thead><tr><th>时间</th><th>成员</th><th>设备</th><th>内容</th><th>动作</th></tr></thead>
              <tbody>
                {dist.data.entries.map((e, i) => (
                  <tr key={i}>
                    <td className="muted">{fmtTime(e.created_at)}</td>
                    <td>{e.email}</td>
                    <td className="muted">{e.hostname || '—'}</td>
                    <td>{e.bundle_name} <span className="muted">v{e.version}</span></td>
                    <td>
                      <span className={`tag ${e.action === 'conflict_skipped' ? 'warn' : e.action === 'remove' ? 'danger' : ''}`}>
                        {e.action}
                      </span>
                      {e.detail && <div className="muted">{e.detail}</div>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </>
  )
}
