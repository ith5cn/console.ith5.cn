import { useEffect, useState, type ReactNode } from 'react'

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    let alive = true
    setLoading(true)
    fn().then(
      (d) => { if (alive) { setData(d); setErr('') } },
      (e) => { if (alive) setErr(e.message ?? String(e)) },
    ).finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce])

  return { data, err, loading, reload: () => setNonce((n) => n + 1) }
}

export function Err({ msg }: { msg: string }) {
  return msg ? <div className="err">{msg}</div> : null
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>
}

export function fmtTime(s?: string) {
  if (!s) return '—'
  const d = new Date(s)
  return d.toLocaleString('zh-CN', { hour12: false })
}

// 授权来源标签：经由权限组 vs 直接授权，一眼可辨
export function Via({ via }: { via: { subject_type: string; group_name?: string }[] }) {
  return (
    <>
      {via.map((v, i) =>
        v.group_name
          ? <span key={i} className="tag group">{v.group_name}</span>
          : <span key={i} className="tag">直接授权 · {v.subject_type}</span>,
      )}
    </>
  )
}

// toCSV 生成可被 Excel 正确打开的 CSV。
//
// 两个容易被忽略的点：
//   - 以 = + - @ 开头的单元格必须转义，否则 Excel 会当公式执行
//     （CSV 注入，技术方案 §16.1）
//   - 中文需要 BOM，否则 Excel 打开是乱码
export function toCSV<T extends object>(rows: T[], headers: [keyof T & string, string][]) {
  const esc = (v: unknown) => {
    let s = v === null || v === undefined ? '' : String(v)
    if (/^[=+\-@]/.test(s)) s = "'" + s
    return '"' + s.replace(/"/g, '""') + '"'
  }
  const lines = [headers.map(([, label]) => esc(label)).join(',')]
  for (const r of rows) {
    lines.push(headers.map(([key]) => esc((r as Record<string, unknown>)[key])).join(','))
  }
  return '\uFEFF' + lines.join('\r\n')
}

export function downloadCSV(filename: string, content: string) {
  const blob = new Blob([content], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
