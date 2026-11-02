export function fmtClock(total: number): string {
  const m = Math.floor(total / 60)
  const s = total % 60
  return (m < 10 ? '0' : '') + m + ':' + (s < 10 ? '0' : '') + s
}

export function plural(n: number, one: string, many?: string): string {
  return n + ' ' + (n === 1 ? one : many || one + 's')
}

export function fmtDur(t: number): string {
  const m = Math.floor(t / 60)
  const s = Math.floor(t % 60)
  return m + ':' + (s < 10 ? '0' : '') + s
}

export function stamp(): string {
  const d = new Date()
  const p = (n: number) => (n < 10 ? '0' : '') + n
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
}
