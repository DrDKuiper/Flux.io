const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

// formatBytes renders a byte count with one decimal and a binary unit.
export function formatBytes(n: number): string {
  if (n < 1) return '0 B'
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), UNITS.length - 1)
  const v = n / Math.pow(1024, i)
  return `${v.toFixed(i === 0 ? 0 : 1)} ${UNITS[i]}`
}

// formatNumber renders counts compactly (1.2K, 3.4M).
export function formatNumber(n: number): string {
  if (n < 1000) return String(n)
  const units = ['', 'K', 'M', 'B']
  const i = Math.min(Math.floor(Math.log10(n) / 3), units.length - 1)
  return `${(n / Math.pow(1000, i)).toFixed(1)}${units[i]}`
}
