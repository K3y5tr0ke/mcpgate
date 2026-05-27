import { useServers } from '../../api/queries'
import type { Policy, Role } from '../../api/types'

const ACTIONS = ['*', 'tools/call', 'tools/list', 'resources/read', 'prompts/get'] as const
type Action = (typeof ACTIONS)[number]

const ACTION_LABELS: Record<Action, string> = {
  '*': 'All',
  'tools/call': 'call',
  'tools/list': 'list',
  'resources/read': 'read',
  'prompts/get': 'prompts',
}

interface Props {
  roles: Role[]
  policies: Policy[]
  onAdd: (subject: string, resource: string, action: string) => Promise<void>
  onRemove: (policy: Policy) => Promise<void>
}

export function PolicyMatrix({ roles, policies, onAdd, onRemove }: Props) {
  const { data: serversData } = useServers()
  const servers = serversData?.servers ?? []

  // Column set: wildcard first, then all known server names.
  const resources = ['*', ...servers.map((s) => s.name)]

  function hasPolicy(subject: string, resource: string, action: Action): boolean {
    return policies.some(
      (p) => p.subject === subject && p.resource === resource && p.action === action,
    )
  }

  function findPolicy(subject: string, resource: string, action: Action): Policy | undefined {
    return policies.find(
      (p) => p.subject === subject && p.resource === resource && p.action === action,
    )
  }

  async function toggle(subject: string, resource: string, action: Action) {
    const existing = findPolicy(subject, resource, action)
    if (existing) {
      await onRemove(existing)
    } else {
      await onAdd(subject, resource, action)
    }
  }

  if (roles.length === 0) return null

  return (
    <div className="overflow-x-auto">
      <table className="min-w-full text-xs border-collapse">
        <thead>
          <tr>
            <th className="sticky left-0 z-10 bg-white border-b border-r border-gray-200 px-3 py-2 text-left font-medium text-gray-500 w-28">
              Role \ Resource
            </th>
            {resources.map((res) => (
              <th
                key={res}
                colSpan={ACTIONS.length}
                className="border-b border-r border-gray-200 px-2 py-2 text-center font-medium text-gray-700 bg-gray-50"
              >
                {res === '*' ? <span className="text-indigo-600">* (all)</span> : res}
              </th>
            ))}
          </tr>
          <tr>
            <th className="sticky left-0 z-10 bg-white border-b border-r border-gray-200" />
            {resources.map((res) =>
              ACTIONS.map((action) => (
                <th
                  key={`${res}:${action}`}
                  className="border-b border-gray-100 px-1 py-1 text-center text-gray-400 font-normal bg-gray-50 min-w-[36px]"
                  title={action}
                >
                  {ACTION_LABELS[action]}
                </th>
              )),
            )}
          </tr>
        </thead>
        <tbody>
          {roles.map((role) => (
            <tr key={role.id} className="hover:bg-gray-50">
              <td className="sticky left-0 z-10 bg-white border-r border-gray-200 px-3 py-1.5 font-medium text-gray-800 whitespace-nowrap">
                {role.name}
              </td>
              {resources.map((res) =>
                ACTIONS.map((action) => {
                  const allowed = hasPolicy(role.name, res, action)
                  return (
                    <td
                      key={`${res}:${action}`}
                      className="border border-gray-100 text-center p-0"
                    >
                      <button
                        type="button"
                        title={`${role.name} → ${res} → ${action}: ${allowed ? 'allowed (click to remove)' : 'denied (click to allow)'}`}
                        onClick={() => toggle(role.name, res, action)}
                        className={`w-full h-7 flex items-center justify-center transition-colors ${
                          allowed
                            ? 'bg-green-50 text-green-600 hover:bg-red-50 hover:text-red-500'
                            : 'text-gray-200 hover:bg-green-50 hover:text-green-400'
                        }`}
                      >
                        {allowed ? (
                          <svg className="h-3.5 w-3.5" viewBox="0 0 16 16" fill="currentColor">
                            <path d="M13.78 4.22a.75.75 0 0 1 0 1.06l-7.25 7.25a.75.75 0 0 1-1.06 0L2.22 9.28a.75.75 0 0 1 1.06-1.06L6 10.94l6.72-6.72a.75.75 0 0 1 1.06 0z" />
                          </svg>
                        ) : (
                          <svg className="h-3 w-3" viewBox="0 0 16 16" fill="currentColor">
                            <path d="M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13zM0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8z" />
                          </svg>
                        )}
                      </button>
                    </td>
                  )
                }),
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
