import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Button } from '../../components/ui/Button'

describe('Button', () => {
  it('renders its label', () => {
    render(<Button>Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
  })

  it('shows a spinner and is disabled when loading', () => {
    render(<Button loading>Save</Button>)
    const btn = screen.getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn.querySelector('svg.animate-spin')).toBeTruthy()
  })

  it('calls onClick when clicked', () => {
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Go</Button>)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('does not call onClick when disabled', () => {
    const onClick = vi.fn()
    render(
      <Button onClick={onClick} disabled>
        Go
      </Button>,
    )
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('applies the danger variant class', () => {
    render(<Button variant="danger">Delete</Button>)
    expect(screen.getByRole('button').className).toContain('bg-red-600')
  })
})
