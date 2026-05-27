import { useState } from 'react'
import { useUsers, usePatchUser, useRoles } from '../../api/queries'
import type { User } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Badge, statusBadge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { DataDrawer } from '../../components/ui/DataDrawer'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'
import { format } from 'date-fns'

export function UsersListPage() {
  const [emailFilter, setEmailFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const { data } = useUsers({ email: emailFilter || undefined, status: statusFilter || undefined })
  const users = data?.users ?? []

  const patchUser = usePatchUser()
  const { data: roles } = useRoles()

  const [selected, setSelected] = useState<User | null>(null)
  const [roleAssign, setRoleAssign] = useState('')

  async function toggleStatus(u: User) {
    const next = u.status === 'active' ? 'suspended' : 'active'
    await patchUser.mutateAsync({ id: u.id, status: next })
    if (selected?.id === u.id) setSelected({ ...u, status: next })
  }

  async function handleAssignRole() {
    if (!selected || !roleAssign) return
    const current = (selected as User & { roles?: string[] }).roles ?? []
    if (current.includes(roleAssign)) return
    await patchUser.mutateAsync({ id: selected.id, roles: [...current, roleAssign] })
    setRoleAssign('')
  }

  const columns: Column<User>[] = [
    {
      key: 'email',
      header: 'Email',
      render: (u) => (
        <button
          onClick={() => setSelected(u)}
          className="text-indigo-600 hover:underline text-left"
        >
          {u.email}
        </button>
      ),
    },
    {
      key: 'display_name',
      header: 'Name',
      render: (u) => u.display_name || <span className="text-gray-400">—</span>,
    },
    {
      key: 'status',
      header: 'Status',
      render: (u) => statusBadge(u.status),
    },
    {
      key: 'idp_name',
      header: 'IdP',
      render: (u) => u.idp_name ? <Badge color="blue">{u.idp_name}</Badge> : <span className="text-gray-400">—</span>,
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (u) => (
        <span className="text-xs text-gray-400">
          {format(new Date(u.created_at), 'yyyy-MM-dd')}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (u) => (
        <Button
          size="sm"
          variant={u.status === 'active' ? 'danger' : 'secondary'}
          onClick={() => toggleStatus(u)}
          loading={patchUser.isPending}
        >
          {u.status === 'active' ? 'Suspend' : 'Activate'}
        </Button>
      ),
    },
  ]

  return (
    <AppShell>
      <div className="space-y-6">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Users</h1>

        <div className="flex gap-3">
          <Input
            placeholder="Filter by email…"
            value={emailFilter}
            onChange={(e) => setEmailFilter(e.target.value)}
            className="w-64"
          />
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            className="h-9 rounded-md border border-gray-300 px-3 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
          >
            <option value="">All statuses</option>
            <option value="active">Active</option>
            <option value="suspended">Suspended</option>
            <option value="deactivated">Deactivated</option>
          </select>
        </div>

        <Card>
          <Table
            columns={columns}
            data={users}
            keyFn={(u) => u.id}
            onRowClick={(u) => setSelected(u)}
          />
          {data?.total !== undefined && (
            <p className="mt-2 text-xs text-gray-400">{data.total} total</p>
          )}
        </Card>
      </div>

      <DataDrawer
        open={!!selected}
        onClose={() => setSelected(null)}
        title={selected?.email}
      >
        {selected && (
          <div className="space-y-5">
            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
                Details
              </h3>
              <dl className="space-y-1.5 text-sm">
                <Row label="ID" value={selected.id} mono />
                <Row label="Display name" value={selected.display_name || '—'} />
                <Row label="Status" value={statusBadge(selected.status)} />
                <Row label="IdP name" value={selected.idp_name || '—'} />
                <Row label="IdP subject" value={selected.idp_subject || '—'} mono />
                <Row
                  label="Created"
                  value={format(new Date(selected.created_at), 'yyyy-MM-dd HH:mm:ss')}
                />
              </dl>
            </section>

            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
                Assign role
              </h3>
              <div className="flex gap-2">
                <select
                  value={roleAssign}
                  onChange={(e) => setRoleAssign(e.target.value)}
                  className="h-9 flex-1 rounded-md border border-gray-300 px-3 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                >
                  <option value="">Select role…</option>
                  {(roles ?? []).map((r) => (
                    <option key={r.id} value={r.name}>
                      {r.name}
                    </option>
                  ))}
                </select>
                <Button size="sm" onClick={handleAssignRole} disabled={!roleAssign}>
                  Assign
                </Button>
              </div>
            </section>

            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
                Actions
              </h3>
              <Button
                variant={selected.status === 'active' ? 'danger' : 'secondary'}
                size="sm"
                onClick={() => toggleStatus(selected)}
                loading={patchUser.isPending}
              >
                {selected.status === 'active' ? 'Suspend user' : 'Activate user'}
              </Button>
            </section>
          </div>
        )}
      </DataDrawer>
    </AppShell>
  )
}

function Row({
  label,
  value,
  mono,
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className="flex gap-2">
      <dt className="w-28 shrink-0 text-gray-500">{label}</dt>
      <dd className={mono ? 'truncate font-mono text-xs text-gray-700' : 'text-gray-700'}>
        {value}
      </dd>
    </div>
  )
}
