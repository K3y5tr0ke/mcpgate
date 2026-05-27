import { useState } from 'react'
import { useIdPs, useCreateIdP, useUpdateIdP, useDeleteIdP } from '../../api/queries'
import type { SAMLIdP } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
import { Input } from '../../components/ui/Input'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'

interface IdPForm {
  name: string
  entity_id: string
  sso_url: string
  slo_url: string
  certificate: string
  is_enabled: boolean
}

const defaultForm = (): IdPForm => ({
  name: '',
  entity_id: '',
  sso_url: '',
  slo_url: '',
  certificate: '',
  is_enabled: true,
})

function idpToForm(idp: SAMLIdP): IdPForm {
  return {
    name: idp.name,
    entity_id: idp.entity_id,
    sso_url: idp.sso_url,
    slo_url: idp.slo_url ?? '',
    certificate: idp.certificate,
    is_enabled: idp.is_enabled,
  }
}

export function IdpListPage() {
  const { data: idps = [], isLoading } = useIdPs()
  const createIdP = useCreateIdP()
  const updateIdP = useUpdateIdP()
  const deleteIdP = useDeleteIdP()

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<SAMLIdP | null>(null)
  const [form, setForm] = useState<IdPForm>(defaultForm())
  const [formError, setFormError] = useState<string | null>(null)

  function openCreate() {
    setEditing(null)
    setForm(defaultForm())
    setFormError(null)
    setModalOpen(true)
  }

  function openEdit(idp: SAMLIdP) {
    setEditing(idp)
    setForm(idpToForm(idp))
    setFormError(null)
    setModalOpen(true)
  }

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const payload = {
      ...form,
      slo_url: form.slo_url.trim() || null,
    }
    try {
      if (editing) {
        await updateIdP.mutateAsync({ id: editing.id, ...payload })
      } else {
        await createIdP.mutateAsync(payload)
      }
      setModalOpen(false)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Save failed')
    }
  }

  async function handleDelete(idp: SAMLIdP) {
    if (!confirm(`Delete IdP "${idp.name}"?`)) return
    await deleteIdP.mutateAsync(idp.id)
  }

  const columns: Column<SAMLIdP>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (idp) => <span className="font-medium text-gray-900">{idp.name}</span>,
    },
    { key: 'entity_id', header: 'Entity ID' },
    {
      key: 'sso_url',
      header: 'SSO URL',
      render: (idp) => (
        <span className="truncate text-xs text-gray-500 max-w-[200px] block">{idp.sso_url}</span>
      ),
    },
    {
      key: 'is_enabled',
      header: 'Status',
      render: (idp) => (
        <Badge color={idp.is_enabled ? 'green' : 'gray'}>
          {idp.is_enabled ? 'enabled' : 'disabled'}
        </Badge>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (idp) => (
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="secondary" onClick={() => openEdit(idp)}>
            Edit
          </Button>
          <Button
            size="sm"
            variant="danger"
            onClick={() => handleDelete(idp)}
            loading={deleteIdP.isPending}
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
          <h1 className="text-xl font-semibold text-gray-900">SAML Identity Providers</h1>
          <Button onClick={openCreate}>Add IdP</Button>
        </div>

        <Card>
          {isLoading ? (
            <p className="text-sm text-gray-400">Loading…</p>
          ) : (
            <Table columns={columns} data={idps} keyFn={(idp) => idp.id} emptyText="No IdPs configured" />
          )}
        </Card>
      </div>

      <Modal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        title={editing ? 'Edit IdP' : 'Add IdP'}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <Input
            label="Name"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
          <Input
            label="Entity ID"
            placeholder="https://idp.example.com/saml"
            value={form.entity_id}
            onChange={(e) => setForm((f) => ({ ...f, entity_id: e.target.value }))}
            required
          />
          <Input
            label="SSO URL"
            placeholder="https://idp.example.com/saml/sso"
            value={form.sso_url}
            onChange={(e) => setForm((f) => ({ ...f, sso_url: e.target.value }))}
            required
          />
          <Input
            label="SLO URL (optional)"
            placeholder="https://idp.example.com/saml/slo"
            value={form.slo_url}
            onChange={(e) => setForm((f) => ({ ...f, slo_url: e.target.value }))}
          />
          <div className="flex flex-col gap-1">
            <label className="text-sm font-medium text-gray-700">Certificate (PEM)</label>
            <textarea
              className="h-32 rounded-md border border-gray-300 px-3 py-2 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500"
              placeholder="-----BEGIN CERTIFICATE-----&#10;…&#10;-----END CERTIFICATE-----"
              value={form.certificate}
              onChange={(e) => setForm((f) => ({ ...f, certificate: e.target.value }))}
              required
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
            <Button type="submit" loading={createIdP.isPending || updateIdP.isPending}>
              {editing ? 'Save' : 'Create'}
            </Button>
          </div>
        </form>
      </Modal>
    </AppShell>
  )
}
