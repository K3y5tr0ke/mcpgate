import { useState } from 'react'
import { useJWKSConfigs, useCreateJWKSConfig, useUpdateJWKSConfig, useDeleteJWKSConfig, useRotateJWKS } from '../../api/queries'
import type { JWKSConfig } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
import { Input } from '../../components/ui/Input'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'

interface JWKSForm {
  name: string
  issuer: string
  jwks_url: string
  audience: string
  is_enabled: boolean
}

const defaultForm = (): JWKSForm => ({
  name: '',
  issuer: '',
  jwks_url: '',
  audience: '',
  is_enabled: true,
})

function configToForm(cfg: JWKSConfig): JWKSForm {
  return {
    name: cfg.name,
    issuer: cfg.issuer,
    jwks_url: cfg.jwks_url,
    audience: cfg.audience,
    is_enabled: cfg.is_enabled,
  }
}

export function JwksListPage() {
  const { data: configs = [], isLoading } = useJWKSConfigs()
  const createConfig = useCreateJWKSConfig()
  const updateConfig = useUpdateJWKSConfig()
  const deleteConfig = useDeleteJWKSConfig()
  const rotateConfig = useRotateJWKS()

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<JWKSConfig | null>(null)
  const [form, setForm] = useState<JWKSForm>(defaultForm())
  const [formError, setFormError] = useState<string | null>(null)

  function openCreate() {
    setEditing(null)
    setForm(defaultForm())
    setFormError(null)
    setModalOpen(true)
  }

  function openEdit(cfg: JWKSConfig) {
    setEditing(cfg)
    setForm(configToForm(cfg))
    setFormError(null)
    setModalOpen(true)
  }

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    try {
      if (editing) {
        await updateConfig.mutateAsync({ id: editing.id, ...form })
      } else {
        await createConfig.mutateAsync(form)
      }
      setModalOpen(false)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Save failed')
    }
  }

  async function handleDelete(cfg: JWKSConfig) {
    if (!confirm(`Delete JWKS config "${cfg.name}"?`)) return
    await deleteConfig.mutateAsync(cfg.id)
  }

  async function handleRotate(cfg: JWKSConfig) {
    try {
      const res = await rotateConfig.mutateAsync(cfg.id)
      alert(`Rotated "${cfg.name}" — issuer advertises ${res.key_count} key(s). Cache evicted.`)
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Rotate failed')
    }
  }

  const columns: Column<JWKSConfig>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (cfg) => <span className="font-medium text-gray-900">{cfg.name}</span>,
    },
    { key: 'issuer', header: 'Issuer' },
    {
      key: 'jwks_url',
      header: 'JWKS URL',
      render: (cfg) => (
        <span className="truncate text-xs text-gray-500 max-w-[200px] block">{cfg.jwks_url}</span>
      ),
    },
    {
      key: 'audience',
      header: 'Audience',
      render: (cfg) => cfg.audience || <span className="text-gray-400">—</span>,
    },
    {
      key: 'is_enabled',
      header: 'Status',
      render: (cfg) => (
        <Badge color={cfg.is_enabled ? 'green' : 'gray'}>
          {cfg.is_enabled ? 'enabled' : 'disabled'}
        </Badge>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (cfg) => (
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="secondary" onClick={() => openEdit(cfg)}>
            Edit
          </Button>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => handleRotate(cfg)}
            loading={rotateConfig.isPending}
          >
            Rotate
          </Button>
          <Button
            size="sm"
            variant="danger"
            onClick={() => handleDelete(cfg)}
            loading={deleteConfig.isPending}
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
          <h1 className="text-xl font-semibold text-gray-900 dark:text-white">JWKS Configs</h1>
          <Button onClick={openCreate}>Add config</Button>
        </div>

        <p className="text-sm text-gray-500">
          Trusted external JWT issuers for M2M authentication. The issuer is matched exactly
          against the <code className="font-mono text-xs">iss</code> claim. Leave audience blank
          to skip the audience check.
        </p>

        <Card>
          {isLoading ? (
            <p className="text-sm text-gray-400">Loading…</p>
          ) : (
            <Table
              columns={columns}
              data={configs}
              keyFn={(cfg) => cfg.id}
              emptyText="No JWKS configs"
            />
          )}
        </Card>
      </div>

      <Modal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        title={editing ? 'Edit JWKS config' : 'Add JWKS config'}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <Input
            label="Name"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
          <Input
            label="Issuer"
            placeholder="https://accounts.google.com"
            value={form.issuer}
            onChange={(e) => setForm((f) => ({ ...f, issuer: e.target.value }))}
            required
          />
          <Input
            label="JWKS URL"
            placeholder="https://accounts.google.com/.well-known/jwks.json"
            value={form.jwks_url}
            onChange={(e) => setForm((f) => ({ ...f, jwks_url: e.target.value }))}
            required
          />
          <Input
            label="Audience (optional)"
            placeholder="https://api.myservice.internal"
            value={form.audience}
            onChange={(e) => setForm((f) => ({ ...f, audience: e.target.value }))}
          />
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
            <Button type="submit" loading={createConfig.isPending || updateConfig.isPending}>
              {editing ? 'Save' : 'Create'}
            </Button>
          </div>
        </form>
      </Modal>
    </AppShell>
  )
}
