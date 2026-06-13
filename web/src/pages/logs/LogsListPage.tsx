import { useState } from 'react'
import { useLogs } from '../../api/queries'
import type { AuditLogEntry } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { statusBadge } from '../../components/ui/badges'
import { Input } from '../../components/ui/Input'
import { Button } from '../../components/ui/Button'
import { DataDrawer } from '../../components/ui/DataDrawer'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'
import { format } from 'date-fns'
import { LiveLogPanel } from '../../components/logs/LiveLogPanel'

async function downloadLogs(
  fmt: 'csv' | 'json',
  params: { search?: string; status?: string; mcp_method?: string },
) {
  const qs = new URLSearchParams({ format: fmt })
  if (params.search) qs.set('search', params.search)
  if (params.status) qs.set('status', params.status)
  if (params.mcp_method) qs.set('mcp_method', params.mcp_method)

  const token = localStorage.getItem('mcpgate_token')
  const res = await fetch(`/api/v1/admin/logs/export?${qs}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) return

  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
  a.download = `mcpgate-logs-${ts}.${fmt}`
  a.click()
  URL.revokeObjectURL(url)
}

const PAGE_SIZE = 50

export function LogsListPage() {
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [methodFilter, setMethodFilter] = useState('')
  const [selected, setSelected] = useState<AuditLogEntry | null>(null)

  const { data, isLoading } = useLogs({
    page,
    page_size: PAGE_SIZE,
    search: search || undefined,
    status: statusFilter || undefined,
    mcp_method: methodFilter || undefined,
  })
  const entries = data?.entries ?? []
  const total = data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const columns: Column<AuditLogEntry>[] = [
    {
      key: 'timestamp',
      header: 'Time',
      render: (e) => (
        <span className="text-xs text-gray-400 tabular-nums">
          {format(new Date(e.timestamp), 'HH:mm:ss.SSS')}
        </span>
      ),
    },
    {
      key: 'user_email',
      header: 'User',
      render: (e) => (
        <span className="truncate text-xs text-gray-700 max-w-[160px] block">{e.user_email}</span>
      ),
    },
    {
      key: 'server_name',
      header: 'Server',
      render: (e) => <Badge color="blue">{e.server_name}</Badge>,
    },
    { key: 'mcp_method', header: 'Method' },
    {
      key: 'tool_name',
      header: 'Tool',
      render: (e) => e.tool_name || <span className="text-gray-400">—</span>,
    },
    {
      key: 'latency_ms',
      header: 'Latency',
      render: (e) => (
        <span className="tabular-nums text-xs">{e.latency_ms} ms</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (e) => statusBadge(e.status),
    },
  ]

  return (
    <AppShell>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Audit logs</h1>
          <div className="flex items-center gap-3">
            <span className="text-sm text-gray-400">{total} entries</span>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => downloadLogs('csv', { search: search || undefined, status: statusFilter || undefined, mcp_method: methodFilter || undefined })}
            >
              Export CSV
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => downloadLogs('json', { search: search || undefined, status: statusFilter || undefined, mcp_method: methodFilter || undefined })}
            >
              Export JSON
            </Button>
          </div>
        </div>

        <div className="flex flex-wrap gap-3">
          <Input
            placeholder="Search…"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setPage(1) }}
            className="w-48"
          />
          <select
            value={statusFilter}
            onChange={(e) => { setStatusFilter(e.target.value); setPage(1) }}
            className="h-9 rounded-md border border-gray-300 px-3 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
          >
            <option value="">All statuses</option>
            <option value="success">Success</option>
            <option value="error">Error</option>
            <option value="denied">Denied</option>
          </select>
          <input
            placeholder="MCP method…"
            value={methodFilter}
            onChange={(e) => { setMethodFilter(e.target.value); setPage(1) }}
            className="h-9 w-36 rounded-md border border-gray-300 px-3 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </div>

        <Card>
          {isLoading ? (
            <p className="text-sm text-gray-400">Loading…</p>
          ) : (
            <>
              <Table
                columns={columns}
                data={entries}
                keyFn={(e) => e.id}
                onRowClick={(e) => setSelected(e)}
                emptyText="No log entries"
              />
              {pageCount > 1 && (
                <div className="mt-3 flex items-center justify-end gap-2">
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={page === 1}
                    onClick={() => setPage((p) => p - 1)}
                  >
                    Prev
                  </Button>
                  <span className="text-xs text-gray-500">
                    {page} / {pageCount}
                  </span>
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={page >= pageCount}
                    onClick={() => setPage((p) => p + 1)}
                  >
                    Next
                  </Button>
                </div>
              )}
            </>
          )}
        </Card>
      </div>

      <LiveLogPanel />

      <DataDrawer
        open={!!selected}
        onClose={() => setSelected(null)}
        title="Log entry"
      >
        {selected && (
          <div className="space-y-4 text-sm">
            <LogRow label="Time" value={format(new Date(selected.timestamp), 'yyyy-MM-dd HH:mm:ss.SSS')} />
            <LogRow label="Trace ID" value={selected.trace_id} mono />
            <LogRow label="Session ID" value={selected.session_id} mono />
            <LogRow label="User" value={selected.user_email} />
            <LogRow label="Client IP" value={selected.client_ip} />
            <LogRow label="Server" value={selected.server_name} />
            <LogRow label="Method" value={selected.mcp_method} />
            <LogRow label="Tool" value={selected.tool_name || '—'} />
            <LogRow label="Direction" value={selected.direction} />
            <LogRow label="Status" value={statusBadge(selected.status)} />
            {selected.error_code !== null && (
              <LogRow label="Error code" value={String(selected.error_code)} />
            )}
            {selected.error_message && (
              <LogRow label="Error message" value={selected.error_message} />
            )}
            <LogRow label="Latency" value={`${selected.latency_ms} ms`} />
            <LogRow label="Req size" value={`${selected.request_size} B`} />
            <LogRow label="Resp size" value={`${selected.response_size} B`} />
            <LogRow label="RBAC decision" value={selected.rbac_decision} />
            <LogRow label="Hash chain" value={selected.hash_chain} mono />
          </div>
        )}
      </DataDrawer>
    </AppShell>
  )
}

function LogRow({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex gap-2">
      <dt className="w-32 shrink-0 text-gray-500">{label}</dt>
      <dd className={mono ? 'truncate font-mono text-xs text-gray-700' : 'text-gray-700 break-all'}>
        {value}
      </dd>
    </div>
  )
}
