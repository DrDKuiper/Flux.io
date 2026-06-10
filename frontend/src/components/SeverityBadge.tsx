// Suricata severity: 1 = high, 2 = medium, 3+ = low.
export function SeverityBadge({ severity }: { severity: number }) {
  const { label, cls } =
    severity === 1
      ? { label: 'high', cls: 'bg-red-900/50 text-red-200' }
      : severity === 2
      ? { label: 'medium', cls: 'bg-amber-900/50 text-amber-200' }
      : { label: 'low', cls: 'bg-blue-900/50 text-blue-200' }
  return <span className={`text-xs px-2 py-0.5 rounded font-medium ${cls}`}>{label}</span>
}
