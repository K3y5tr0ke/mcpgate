import { useState } from 'react'
import {
  useServers,
  useCreateServer,
  useUpdateServer,
  useDeleteServer,
  useTestServer,
} from '../../api/queries'
import type { MCPServer } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
import { Input } from '../../components/ui/Input'
import { Select } from '../../components/ui/Select'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'

const TRANSPORT_OPTIONS = [
  { value: 'streamable_http', label: 'Streamable HTTP' },
  { value: 'sse', label: 'SSE' },
  { value: 'stdio', label: 'stdio' },
]

interface ServerFormState {
  name: string
  description: string
  transport: MCPServer['transport']
  config: string
  is_enabled: boolean
}

const defaultForm = (): ServerFormState => ({
  name: '',
  description: '',
  transport: 'streamable_http',
  config: '{}',
  is_enabled: true,
})

function serverToForm(s: MCPServer): ServerFormState {
  return {
    name: s.name,
    description: s.description,
    transport: s.transport,
    config: JSON.stringify(s.config, null, 2),
    is_enabled: s.is_enabled,
  }
}

export function ServersListPage() {
  const { data, isLoading } = useServers()
  const servers = data?.servers ?? []

  const createServer = useCreateServer()
  const updateServer = useUpdateServer()
  const deleteServer = useDeleteServer()
  const testServer = useTestServer()

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<MCPServer | null>(null)
  const [form, setForm] = useState<ServerFormState>(defaultForm())
  const [formError, setFormError] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<Record<string, { ok: boolean; error?: string }>>({})

  function openCreate() {
    setEditing(null)
    setForm(defaultForm())
    setFormError(null)
    setModalOpen(true)
  }

  function openEdit(s: MCPServer) {
    setEditing(s)
    setForm(serverToForm(s))
    setFormError(null)
    setModalOpen(true)
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    let config: Record<string, unknown>
    try {
      config = JSON.parse(form.config)
    } catch {
      setFormError('Config must be valid JSON')
      return
    }
    const payload = {
      name: form.name,
      description: form.description,
      transport: form.transport,
      config,
      is_enabled: form.is_enabled,
    }
    try {
      if (editing) {
        await updateServer.mutateAsync({ id: editing.id, ...payload })
      } else {
        await createServer.mutateAsync(payload)
      }
      setModalOpen(false)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Save failed')
    }
  }

  async function handleDelete(s: MCPServer) {
    if (!confirm(`Delete server "${s.name}"?`)) return
    await deleteServer.mutateAsync(s.id)
  }

  async function handleTest(s: MCPServer) {
    const result = await testServer.mutateAsync(s.id)
    setTestResult((prev) => ({ ...prev, [s.id]: result }))
  }

  const columns: Column<MCPServer>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (s) => <span className="font-medium text-gray-900">{s.name}</span>,
    },
    { key: 'transport', header: 'Transport' },
    {
      key: 'is_enabled',
      header: 'Status',
      render: (s) => (
        <Badge color={s.is_enabled ? 'green' : 'gray'}>
          {s.is_enabled ? 'enabled' : 'disabled'}
        </Badge>
      ),
    },
    {
      key: 'test',
      header: 'Connectivity',
      render: (s) => {
        const r = testResult[s.id]
        return (
          <div className="flex items-center gap-2">
            {r && (
              <Badge color={r.ok ? 'green' : 'red'}>{r.ok ? 'ok' : r.error ?? 'fail'}</Badge>
            )}
            <Button size="sm" variant="ghost" onClick={() => handleTest(s)}>
              Test
            </Button>
          </div>
        )
      },
    },
    {
      key: 'actions',
      header: '',
      render: (s) => (
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="secondary" onClick={() => openEdit(s)}>
            Edit
          </Button>
          <Button
            size="sm"
            variant="danger"
            onClick={() => handleDelete(s)}
            loading={deleteServer.isPending}
          >
            Delete
          </Button>
        </div>
      ),
    },
  ]

  return (
    <AppShell>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold text-gray-900">Servers</h1>
          <Button onClick={openCreate}>Add server</Button>
        </div>

        <Card>
          {isLoading ? (
            <p className="text-sm text-gray-400">Loading…</p>
          ) : (
            <Table columns={columns} data={servers} keyFn={(s) => s.id} />
          )}
        </Card>
      </div>

      <Modal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        title={editing ? 'Edit server' : 'Add server'}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <Input
            label="Name"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
          <Input
            label="Description"
            value={form.description}
            onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
          />
          <Select
            label="Transport"
            options={TRANSPORT_OPTIONS}
            value={form.transport}
            onChange={(e) =>
              setForm((f) => ({ ...f, transport: e.target.value as MCPServer['transport'] }))
            }
          />
          <div className="flex flex-col gap-1">
            <label className="text-sm font-medium text-gray-700">Config (JSON)</label>
            <textarea
              className="h-28 rounded-md border border-gray-300 px-3 py-2 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={form.config}
              onChange={(e) => setForm((f) => ({ ...f, config: e.target.value }))}
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-gray-700">
            <input
              type="checkbox"
              checked={form.is_enabled}
              onChange={(e) => setForm((f) => ({ ...f, is_enabled: e.target.checked }))}
            />
            Enabled
          </label>
          {formError && <p className="text-xs text-red-500">{formError}</p>}
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="secondary" type="button" onClick={() => setModalOpen(false)}>
              Cancel
            </Button>
            <Button
              type="submit"
              loading={createServer.isPending || updateServer.isPending}
            >
              {editing ? 'Save' : 'Create'}
            </Button>
          </div>
        </form>
      </Modal>
    </AppShell>
  )
}
