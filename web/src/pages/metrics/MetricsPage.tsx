import { useMetricsSummary, useMetricsTimeseries } from '../../api/queries'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'

function StatCard({ label, value, sub }: { label: string; value: React.ReactNode; sub?: string }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5">
      <p className="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</p>
      <p className="mt-1 text-2xl font-semibold text-gray-900 dark:text-white">{value}</p>
      {sub && <p className="mt-0.5 text-xs text-gray-400">{sub}</p>}
    </div>
  )
}

function Sparkline({ buckets }: { buckets: { requests: number }[] }) {
  if (buckets.length === 0) return null
  const max = Math.max(...buckets.map((b) => b.requests), 1)
  const h = 48
  const w = 4
  const gap = 2

  return (
    <svg
      width={buckets.length * (w + gap)}
      height={h}
      className="block"
      aria-hidden="true"
    >
      {buckets.map((b, i) => {
        const barH = Math.max(2, Math.round((b.requests / max) * h))
        return (
          <rect
            key={i}
            x={i * (w + gap)}
            y={h - barH}
            width={w}
            height={barH}
            rx={1}
            className="fill-indigo-400"
          />
        )
      })}
    </svg>
  )
}

export function MetricsPage() {
  const { data: summary, isLoading: summaryLoading } = useMetricsSummary()
  const { data: tsData } = useMetricsTimeseries()
  const buckets = tsData?.buckets ?? []

  return (
    <AppShell>
      <div className="space-y-6">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Metrics</h1>

        {summaryLoading ? (
          <p className="text-sm text-gray-400">Loading…</p>
        ) : summary ? (
          <>
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <StatCard
                label="Req / min"
                value={summary.requests_per_minute.toFixed(0)}
                sub="previous complete minute"
              />
              <StatCard
                label="Error rate"
                value={`${summary.error_rate_percent.toFixed(1)}%`}
                sub="4xx + 5xx"
              />
              <StatCard
                label="p95 latency"
                value={`${summary.p95_latency_ms.toFixed(0)} ms`}
              />
              <StatCard
                label="Active requests"
                value={summary.active_requests}
              />
            </div>

            <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
              <StatCard
                label="Active sessions"
                value={summary.active_sessions}
              />
              <StatCard
                label="Dropped audit logs"
                value={summary.dropped_audit_logs}
                sub={summary.dropped_audit_logs > 0 ? 'pipeline buffer full' : 'healthy'}
              />
            </div>

            {summary.circuit_breakers.length > 0 && (
              <Card title="Circuit breakers">
                <div className="flex flex-wrap gap-2">
                  {summary.circuit_breakers.map((cb) => (
                    <div key={cb.server} className="flex items-center gap-2">
                      <span className="text-sm text-gray-700">{cb.server}</span>
                      <Badge
                        color={
                          cb.state === 'closed' ? 'green' : cb.state === 'open' ? 'red' : 'yellow'
                        }
                      >
                        {cb.state}
                      </Badge>
                    </div>
                  ))}
                </div>
              </Card>
            )}
          </>
        ) : null}

        <Card title="Requests — last 60 minutes">
          {buckets.length === 0 ? (
            <p className="text-sm text-gray-400">No data yet.</p>
          ) : (
            <div className="overflow-x-auto">
              <Sparkline buckets={buckets} />
              <p className="mt-1 text-xs text-gray-400">
                Each bar = 1 minute. Tallest bar ={' '}
                {Math.max(...buckets.map((b) => b.requests))} req.
              </p>
            </div>
          )}
        </Card>
      </div>
    </AppShell>
  )
}
