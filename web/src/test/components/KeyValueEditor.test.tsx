import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { KeyValueEditor } from '../../components/ui/KeyValueEditor'

describe('KeyValueEditor', () => {
  it('adds a new pair via the add row', () => {
    const onChange = vi.fn()
    render(<KeyValueEditor label="Attrs" value={{}} onChange={onChange} />)

    fireEvent.change(screen.getByLabelText('Attrs new key'), { target: { value: 'email' } })
    fireEvent.change(screen.getByLabelText('Attrs new value'), { target: { value: 'mail' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add' }))

    expect(onChange).toHaveBeenCalledWith({ email: 'mail' })
  })

  it('does not add a pair with an empty key', () => {
    const onChange = vi.fn()
    render(<KeyValueEditor label="Attrs" value={{}} onChange={onChange} />)
    // Add button is disabled with an empty key.
    expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  })

  it('edits an existing value', () => {
    const onChange = vi.fn()
    render(<KeyValueEditor label="Attrs" value={{ email: 'mail' }} onChange={onChange} />)

    fireEvent.change(screen.getByLabelText('Attrs value'), { target: { value: 'upn' } })
    expect(onChange).toHaveBeenCalledWith({ email: 'upn' })
  })

  it('removes a pair', () => {
    const onChange = vi.fn()
    render(<KeyValueEditor label="Attrs" value={{ email: 'mail', group: 'role' }} onChange={onChange} />)

    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0])
    expect(onChange).toHaveBeenCalledWith({ group: 'role' })
  })
})
