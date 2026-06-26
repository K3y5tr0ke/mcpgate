import { Badge } from './Badge'

// Badge helper renderers. Kept in their own (component-free) module so Badge.tsx
// exports only the Badge component — required for React Fast Refresh.

export function circuitStateBadge(state: string) {
  if (state === 'closed') return <Badge color="green">closed</Badge>
  if (state === 'open') return <Badge color="red">open</Badge>
  if (state === 'half-open') return <Badge color="yellow">half-open</Badge>
  return <Badge color="gray">{state}</Badge>
}

export function statusBadge(status: string) {
  if (status === 'active') return <Badge color="green">active</Badge>
  if (status === 'suspended') return <Badge color="red">suspended</Badge>
  if (status === 'success') return <Badge color="green">success</Badge>
  if (status === 'error') return <Badge color="red">error</Badge>
  if (status === 'denied') return <Badge color="yellow">denied</Badge>
  return <Badge color="gray">{status}</Badge>
}
