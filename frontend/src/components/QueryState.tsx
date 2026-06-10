import { ReactNode } from 'react'

interface Props {
  isLoading: boolean
  isError: boolean
  isEmpty?: boolean
  onRetry?: () => void
  children: ReactNode
}

// QueryState renders loading/error/empty fallbacks around query-backed content.
export function QueryState({ isLoading, isError, isEmpty, onRetry, children }: Props) {
  if (isLoading) {
    return <div className="animate-pulse text-gray-500 text-sm p-4">Loading…</div>
  }
  if (isError) {
    return (
      <div className="text-red-300 text-sm p-4 bg-red-950/40 rounded border border-red-900/50 flex items-center gap-3">
        <span>Failed to load.</span>
        {onRetry && (
          <button onClick={onRetry} className="underline hover:text-red-200">
            Retry
          </button>
        )}
      </div>
    )
  }
  if (isEmpty) {
    return <div className="text-gray-500 text-sm p-4">No data for this range.</div>
  }
  return <>{children}</>
}
