export function StatCard({ label, value, danger }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
      <div className="text-gray-400 text-sm">{label}</div>
      <div className={`text-2xl font-bold mt-1 ${danger ? 'text-red-400' : 'text-white'}`}>{value}</div>
    </div>
  )
}
