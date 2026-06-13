import { useMetricsSummary, useMetricsTimeseries, useLogs } from '../../api/queries'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { circuitStateBadge, statusBadge } from '../../components/ui/badges'
import { SparklineChart } from '../../components/charts/SparklineChart'
import { AppShell } from '../../components/layout/AppShell'
import { format } from 'date-fns'

function MetricCard({
  label,
  value,
  sub,
}: {
  label: string
  value: string | number
  sub?: string
}) {
  return (
    <Card>
      <p className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</p>
      <p className="mt-1 text-2xl font-bold text-gray-900">{value}</p>
      {sub && <p className="text-xs text-gray-400">{sub}</p>}
    </Card>
  )
}

export function DashboardPage() {
  const { data: summary, isLoading: loadingMetrics } = useMetricsSummary()
  const { data: ts } = useMetricsTimeseries()
  const { data: logsData } = useLogs({ page: 1, page_size: 20 })

  const sparkData = ts?.buckets?.map((b) => b.requests) ?? []

  return (
    <AppShell>
      <div className="space-y-6">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Dashboard</h1>

        {/* Metrics strip */}
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
          <MetricCard
            label="Requests / min"
            value={loadingMetrics ? '…' : (summary?.requests_per_minute ?? 0).toFixed(0)}
          />
          <MetricCard
            label="Error rate"
            value={loadingMetrics ? '…' : `${(summary?.error_rate_percent ?? 0).toFixed(1)}%`}
          />
          <MetricCard
            label="p95 latency"
            value={loadingMetrics ? '…' : `${(summary?.p95_latency_ms ?? 0).toFixed(0)} ms`}
          />
          <MetricCard
            label="Active sessions"
            value={loadingMetrics ? '…' : (summary?.active_sessions ?? 0)}
          />
        </div>

        {/* Sparkline */}
        {sparkData.length > 1 && (
          <Card title="Requests (last hour)">
            <SparklineChart data={sparkData} />
          </Card>
        )}

        {/* Server health grid */}
        {summary && summary.circuit_breakers.length > 0 && (
          <Card title="Server health">
            <div className="flex flex-wrap gap-3">
              {summary.circuit_breakers.map((cb) => (
                <div
                  key={cb.server}
                  className="flex items-center gap-2 rounded border border-gray-100 px-3 py-2"
                >
                  <span className="text-sm text-gray-700">{cb.server}</span>
                  {circuitStateBadge(cb.state)}
                </div>
              ))}
            </div>
          </Card>
        )}

        {/* Activity feed */}
        <Card title="Recent activity">
          {logsData?.entries?.length === 0 ? (
            <p className="text-sm text-gray-400">No recent activity</p>
          ) : (
            <ul className="divide-y divide-gray-50">
              {(logsData?.entries ?? []).map((e) => (
                <li key={e.id} className="flex items-center gap-3 py-2 text-sm">
                  <span className="w-32 truncate text-gray-400 text-xs">
                    {format(new Date(e.timestamp), 'HH:mm:ss')}
                  </span>
                  <span className="w-40 truncate text-gray-700">{e.user_email}</span>
                  <Badge color="blue">{e.server_name}</Badge>
                  <span className="text-gray-500">{e.mcp_method}</span>
                  {statusBadge(e.status)}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </AppShell>
  )
}
