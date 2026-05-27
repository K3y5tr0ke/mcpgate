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
  is_enabled: boolean
  // streamable_http
  endpoint: string
  // sse
  sseEndpoint: string
  messageEndpoint: string
  // stdio
  command: string
  args: string // space-separated
}

const defaultForm = (): ServerFormState => ({
  name: '',
  description: '',
  transport: 'streamable_http',
  is_enabled: true,
  endpoint: '',
  sseEndpoint: '',
  messageEndpoint: '',
  command: '',
  args: '',
})

function serverToForm(s: MCPServer): ServerFormState {
  const cfg = s.config ?? {}
  return {
    name: s.name,
    description: s.description,
    transport: s.transport,
    is_enabled: s.is_enabled,
    endpoint: (cfg['endpoint'] as string) || (cfg['url'] as string) || '',
    sseEndpoint: (cfg['sse_endpoint'] as string) || '',
    messageEndpoint: (cfg['message_endpoint'] as string) || '',
    command: (cfg['command'] as string) || '',
    args: Array.isArray(cfg['args']) ? (cfg['args'] as string[]).join(' ') : '',
  }
}

function formToConfig(f: ServerFormState): Record<string, unknown> {
  switch (f.transport) {
    case 'streamable_http':
      return { endpoint: f.endpoint.trim() }
    case 'sse':
      return { sse_endpoint: f.sseEndpoint.trim(), message_endpoint: f.messageEndpoint.trim() }
    case 'stdio': {
      const args = f.args.trim() ? f.args.trim().split(/\s+/) : []
      return { command: f.command.trim(), ...(args.length ? { args } : {}) }
    }
    default:
      return {}
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

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const payload = {
      name: form.name,
      description: form.description,
      transport: form.transport,
      config: formToConfig(form),
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
          {form.transport === 'streamable_http' && (
            <Input
              label="Endpoint URL"
              placeholder="https://my-mcp-server/mcp"
              value={form.endpoint}
              onChange={(e) => setForm((f) => ({ ...f, endpoint: e.target.value }))}
              required
            />
          )}
          {form.transport === 'sse' && (
            <>
              <Input
                label="SSE Endpoint"
                placeholder="https://my-mcp-server/sse"
                value={form.sseEndpoint}
                onChange={(e) => setForm((f) => ({ ...f, sseEndpoint: e.target.value }))}
                required
              />
              <Input
                label="Message Endpoint"
                placeholder="https://my-mcp-server/message"
                value={form.messageEndpoint}
                onChange={(e) => setForm((f) => ({ ...f, messageEndpoint: e.target.value }))}
                required
              />
            </>
          )}
          {form.transport === 'stdio' && (
            <>
              <Input
                label="Command"
                placeholder="npx"
                value={form.command}
                onChange={(e) => setForm((f) => ({ ...f, command: e.target.value }))}
                required
              />
              <Input
                label="Arguments (space-separated)"
                placeholder="-y @modelcontextprotocol/server-filesystem /tmp"
                value={form.args}
                onChange={(e) => setForm((f) => ({ ...f, args: e.target.value }))}
              />
            </>
          )}
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
