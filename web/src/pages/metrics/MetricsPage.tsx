import { useState } from 'react'
import {
  Area,
  AreaChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { useMetricsSummary, useMetricsTimeseries, useServers } from '../../api/queries'
import type { MetricsBucket } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'

const WINDOWS = ['1h', '6h', '24h', '7d'] as const
type Window = (typeof WINDOWS)[number]

const selectClasses =
  'h-9 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 ' +
  'focus:outline-none focus:ring-2 focus:ring-indigo-500 ' +
  'dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100'

// formatTs renders a bucket's unix-minute as an axis label. Wider windows
// (coarser resolution) include the date; intraday windows show only HH:mm.
function makeTsFormatter(resolutionSecs: number) {
  const wide = resolutionSecs >= 900 // 15m buckets and up → 24h/7d windows
  return (minute: number) => {
    const d = new Date(minute * 60 * 1000)
    const hh = String(d.getHours()).padStart(2, '0')
    const mm = String(d.getMinutes()).padStart(2, '0')
    if (wide) {
      const mo = String(d.getMonth() + 1).padStart(2, '0')
      const day = String(d.getDate()).padStart(2, '0')
      return `${mo}/${day} ${hh}:${mm}`
    }
    return `${hh}:${mm}`
  }
}

function MetricsLineChart({
  data,
  label,
  resolutionSecs,
}: {
  data: MetricsBucket[]
  label: string
  resolutionSecs: number
}) {
  const fmt = makeTsFormatter(resolutionSecs)
  return (
    <ResponsiveContainer width="100%" height={160}>
      <AreaChart data={data} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id="fillReq" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="#6366f1" stopOpacity={0.3} />
            <stop offset="95%" stopColor="#6366f1" stopOpacity={0} />
          </linearGradient>
        </defs>
        <XAxis
          dataKey="minute"
          tickFormatter={fmt}
          tick={{ fontSize: 10 }}
          axisLine={false}
          tickLine={false}
          minTickGap={32}
        />
        <YAxis width={28} tick={{ fontSize: 10 }} axisLine={false} tickLine={false} allowDecimals={false} />
        <Tooltip
          formatter={(v) => [v as number, label]}
          labelFormatter={(m) => fmt(m as number)}
        />
        <Area
          type="monotone"
          dataKey="requests"
          stroke="#6366f1"
          fill="url(#fillReq)"
          strokeWidth={1.5}
          dot={false}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}

function StatCard({ label, value, sub }: { label: string; value: React.ReactNode; sub?: string }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-800">
      <p className="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</p>
      <p className="mt-1 text-2xl font-semibold text-gray-900 dark:text-white">{value}</p>
      {sub && <p className="mt-0.5 text-xs text-gray-400">{sub}</p>}
    </div>
  )
}

function ComingSoonChart({ title }: { title: string }) {
  return (
    <Card title={title}>
      <div className="flex h-[160px] items-center justify-center text-sm text-gray-400">
        Time-series coming soon
      </div>
    </Card>
  )
}

function WindowTabs({ value, onChange }: { value: Window; onChange: (w: Window) => void }) {
  return (
    <div className="inline-flex rounded-md border border-gray-300 dark:border-gray-600">
      {WINDOWS.map((w) => (
        <button
          key={w}
          type="button"
          onClick={() => onChange(w)}
          className={
            'px-3 py-1.5 text-sm first:rounded-l-md last:rounded-r-md ' +
            (w === value
              ? 'bg-indigo-600 text-white'
              : 'bg-white text-gray-700 hover:bg-gray-50 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700')
          }
        >
          {w}
        </button>
      ))}
    </div>
  )
}

function PerServerSection({ window }: { window: Window }) {
  const { data: serversData } = useServers()
  const servers = serversData?.servers ?? []
  const [selected, setSelected] = useState('')

  if (servers.length === 0) return null
  const current = selected || servers[0].name

  return <PerServerChart window={window} server={current} servers={servers.map((s) => s.name)} selected={current} onSelect={setSelected} />
}

function PerServerChart({
  window,
  server,
  servers,
  selected,
  onSelect,
}: {
  window: Window
  server: string
  servers: string[]
  selected: string
  onSelect: (s: string) => void
}) {
  const { data } = useMetricsTimeseries(window, server)
  const buckets = data?.buckets ?? []
  const hasTraffic = buckets.some((b) => b.requests > 0)

  return (
    <Card title="Per-server request volume">
      <div className="mb-3 flex items-center gap-2">
        <label className="text-sm text-gray-600 dark:text-gray-300">Server</label>
        <select className={selectClasses} value={selected} onChange={(e) => onSelect(e.target.value)}>
          {servers.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </div>
      <MetricsLineChart data={buckets} label={`requests (${server})`} resolutionSecs={data?.resolution_secs ?? 60} />
      {!hasTraffic && (
        <p className="mt-1 text-xs text-gray-400">No traffic in this window.</p>
      )}
    </Card>
  )
}

export function MetricsPage() {
  const [window, setWindow] = useState<Window>('1h')
  const { data: summary, isLoading: summaryLoading } = useMetricsSummary()
  const { data: tsData } = useMetricsTimeseries(window)
  const buckets = tsData?.buckets ?? []
  const resolutionSecs = tsData?.resolution_secs ?? 60

  return (
    <AppShell>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Metrics</h1>
          <WindowTabs value={window} onChange={setWindow} />
        </div>

        {summaryLoading ? (
          <p className="text-sm text-gray-400">Loading…</p>
        ) : summary ? (
          <>
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <StatCard label="Req / min" value={summary.requests_per_minute.toFixed(0)} sub="previous complete minute" />
              <StatCard label="Error rate" value={`${summary.error_rate_percent.toFixed(1)}%`} sub="4xx + 5xx" />
              <StatCard label="p95 latency" value={`${summary.p95_latency_ms.toFixed(0)} ms`} />
              <StatCard label="Active requests" value={summary.active_requests} />
            </div>

            <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
              <StatCard label="Active sessions" value={summary.active_sessions} />
              <StatCard
                label="Dropped audit logs"
                value={summary.dropped_audit_logs}
                sub={summary.dropped_audit_logs > 0 ? 'pipeline buffer full' : 'healthy'}
              />
            </div>
          </>
        ) : null}

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <Card title="Request volume">
            {buckets.length === 0 ? (
              <div className="flex h-[160px] items-center justify-center text-sm text-gray-400">No data yet.</div>
            ) : (
              <MetricsLineChart data={buckets} label="requests" resolutionSecs={resolutionSecs} />
            )}
          </Card>
          <ComingSoonChart title="Error rate (%)" />
          <ComingSoonChart title="p95 latency (ms)" />
        </div>

        <PerServerSection window={window} />

        {summary && summary.circuit_breakers.length > 0 && (
          <Card title="Circuit breakers">
            <div className="flex flex-wrap gap-2">
              {summary.circuit_breakers.map((cb) => (
                <div key={cb.server} className="flex items-center gap-2">
                  <span className="text-sm text-gray-700 dark:text-gray-200">{cb.server}</span>
                  <Badge color={cb.state === 'closed' ? 'green' : cb.state === 'open' ? 'red' : 'yellow'}>
                    {cb.state}
                  </Badge>
                </div>
              ))}
            </div>
          </Card>
        )}
      </div>
    </AppShell>
  )
}
