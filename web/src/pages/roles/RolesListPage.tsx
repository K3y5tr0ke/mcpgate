import { useState } from 'react'
import {
  useRoles,
  useCreateRole,
  useDeleteRole,
  usePolicies,
  useAddPolicy,
  useRemovePolicy,
} from '../../api/queries'
import type { Role, Policy } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
import { Input } from '../../components/ui/Input'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'
import { PolicyMatrix } from '../../components/roles/PolicyMatrix'

const ACTIONS = ['tools/call', 'tools/list', 'resources/read', 'prompts/get', '*']

export function RolesListPage() {
  const { data: roles = [] } = useRoles()
  const { data: policies = [] } = usePolicies()
  const createRole = useCreateRole()
  const deleteRole = useDeleteRole()
  const addPolicy = useAddPolicy()
  const removePolicy = useRemovePolicy()

  const [createOpen, setCreateOpen] = useState(false)
  const [newRoleName, setNewRoleName] = useState('')
  const [newRoleDesc, setNewRoleDesc] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)

  // Policy editor state
  const [policyRole, setPolicyRole] = useState('')
  const [policyResource, setPolicyResource] = useState('')
  const [policyAction, setPolicyAction] = useState('*')

  async function handleCreate(e: React.SyntheticEvent) {
    e.preventDefault()
    try {
      await createRole.mutateAsync({ name: newRoleName, description: newRoleDesc })
      setCreateOpen(false)
      setNewRoleName('')
      setNewRoleDesc('')
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Failed')
    }
  }

  async function handleDelete(r: Role) {
    if (r.is_system) return
    if (!confirm(`Delete role "${r.name}"?`)) return
    await deleteRole.mutateAsync(r.id)
  }

  async function handleAddPolicy(e: React.SyntheticEvent) {
    e.preventDefault()
    if (!policyRole || !policyResource) return
    await addPolicy.mutateAsync({
      subject: policyRole,
      resource: policyResource,
      action: policyAction,
      effect: 'allow',
    })
    setPolicyResource('')
  }

  async function handleRemovePolicy(p: Policy) {
    await removePolicy.mutateAsync(p)
  }

  const roleColumns: Column<Role>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (r) => (
        <span className="font-medium text-gray-900 dark:text-white">
          {r.name}
          {r.is_system && (
            <Badge color="purple" className="ml-2">
              system
            </Badge>
          )}
        </span>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      render: (r) => r.description || <span className="text-gray-400">—</span>,
    },
    {
      key: 'actions',
      header: '',
      render: (r) => (
        <Button
          size="sm"
          variant="danger"
          disabled={r.is_system}
          onClick={() => handleDelete(r)}
          loading={deleteRole.isPending}
        >
          Delete
        </Button>
      ),
    },
  ]

  const policyColumns: Column<Policy>[] = [
    { key: 'subject', header: 'Role' },
    { key: 'resource', header: 'Resource' },
    { key: 'action', header: 'Action' },
    {
      key: 'effect',
      header: 'Effect',
      render: (p) => (
        <Badge color={p.effect === 'allow' ? 'green' : 'red'}>{p.effect}</Badge>
      ),
    },
    {
      key: 'remove',
      header: '',
      render: (p) => (
        <Button size="sm" variant="ghost" onClick={() => handleRemovePolicy(p)}>
          Remove
        </Button>
      ),
    },
  ]

  return (
    <AppShell>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Roles</h1>
          <Button onClick={() => setCreateOpen(true)}>New role</Button>
        </div>

        <Card title="Role list">
          <Table columns={roleColumns} data={roles} keyFn={(r) => r.id} />
        </Card>

        <Card title="Policy matrix">
          <p className="mb-3 text-xs text-gray-400">Click a cell to toggle allow/deny. Rows = roles, columns = servers + wildcard (*). Actions: All / call / list / read / prompts.</p>
          <PolicyMatrix
            roles={roles}
            policies={policies}
            onAdd={(subject, resource, action) =>
              addPolicy.mutateAsync({ subject, resource, action, effect: 'allow' })
            }
            onRemove={handleRemovePolicy}
          />
        </Card>

        <Card title="Access policies (raw)">
          <form onSubmit={handleAddPolicy} className="mb-4 flex flex-wrap gap-2 items-end">
            <div className="flex flex-col gap-1">
              <label className="text-xs text-gray-500 dark:text-gray-400">Role</label>
              <select
                value={policyRole}
                onChange={(e) => setPolicyRole(e.target.value)}
                className="h-9 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                required
              >
                <option value="">Select role…</option>
                {roles.map((r) => (
                  <option key={r.id} value={r.name}>
                    {r.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-gray-500 dark:text-gray-400">Resource (server name or *)</label>
              <input
                value={policyResource}
                onChange={(e) => setPolicyResource(e.target.value)}
                placeholder="e.g. my-server"
                className="h-9 w-40 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                required
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-gray-500 dark:text-gray-400">Action</label>
              <select
                value={policyAction}
                onChange={(e) => setPolicyAction(e.target.value)}
                className="h-9 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
              >
                {ACTIONS.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </select>
            </div>
            <Button type="submit" size="sm" loading={addPolicy.isPending}>
              Add allow
            </Button>
          </form>

          <Table
            columns={policyColumns}
            data={policies}
            keyFn={(p) => `${p.subject}:${p.resource}:${p.action}`}
            emptyText="No policies defined"
          />
        </Card>
      </div>

      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="New role"
      >
        <form onSubmit={handleCreate} className="space-y-4">
          <Input
            label="Name"
            value={newRoleName}
            onChange={(e) => setNewRoleName(e.target.value)}
            required
          />
          <Input
            label="Description"
            value={newRoleDesc}
            onChange={(e) => setNewRoleDesc(e.target.value)}
          />
          {createError && <p className="text-xs text-red-500">{createError}</p>}
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="secondary" type="button" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={createRole.isPending}>
              Create
            </Button>
          </div>
        </form>
      </Modal>
    </AppShell>
  )
}
