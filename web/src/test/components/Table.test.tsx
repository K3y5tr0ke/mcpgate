import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Table } from '../../components/ui/Table'
import type { Column } from '../../components/ui/Table'

interface Row {
  id: string
  name: string
}

const columns: Column<Row>[] = [
  { key: 'name', header: 'Name' },
  { key: 'id', header: 'ID' },
]

describe('Table', () => {
  it('renders header columns', () => {
    render(<Table columns={columns} data={[]} />)
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('ID')).toBeInTheDocument()
  })

  it('renders one row per data item', () => {
    const data: Row[] = [
      { id: '1', name: 'alpha' },
      { id: '2', name: 'beta' },
    ]
    render(<Table columns={columns} data={data} keyFn={(r) => r.id} />)
    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.getByText('beta')).toBeInTheDocument()
  })

  it('renders emptyText when there is no data', () => {
    render(<Table columns={columns} data={[]} emptyText="Nothing here" />)
    expect(screen.getByText('Nothing here')).toBeInTheDocument()
  })

  it('calls onRowClick when a row is clicked', () => {
    const onRowClick = vi.fn()
    const data: Row[] = [{ id: '1', name: 'alpha' }]
    render(<Table columns={columns} data={data} keyFn={(r) => r.id} onRowClick={onRowClick} />)
    fireEvent.click(screen.getByText('alpha'))
    expect(onRowClick).toHaveBeenCalledWith(data[0])
  })
})
