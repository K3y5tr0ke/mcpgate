import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Modal } from '../../components/ui/Modal'

describe('Modal', () => {
  it('is closed when open is false', () => {
    render(
      <Modal open={false} onClose={() => {}} title="Hi">
        <p>body</p>
      </Modal>,
    )
    const dialog = document.querySelector('dialog')
    expect(dialog?.open).toBe(false)
  })

  it('renders title and children and opens when open is true', () => {
    render(
      <Modal open onClose={() => {}} title="My Title">
        <p>hello body</p>
      </Modal>,
    )
    expect(screen.getByText('My Title')).toBeInTheDocument()
    expect(screen.getByText('hello body')).toBeInTheDocument()
    expect(document.querySelector('dialog')?.open).toBe(true)
  })

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn()
    render(
      <Modal open onClose={onClose} title="T">
        <p>x</p>
      </Modal>,
    )
    fireEvent.click(screen.getByRole('button', { name: '✕' }))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('calls onClose when the dialog emits close (Escape)', () => {
    const onClose = vi.fn()
    render(
      <Modal open onClose={onClose} title="T">
        <p>x</p>
      </Modal>,
    )
    const dialog = document.querySelector('dialog')!
    fireEvent(dialog, new Event('close'))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
