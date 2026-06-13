import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { IdpWizard } from '../../components/idp/IdpWizard'

describe('IdpWizard', () => {
  it('gates Next on the current step validation', () => {
    render(<IdpWizard open onClose={() => {}} onSubmit={vi.fn()} />)

    // Step 1 requires a name — Next is disabled until it is filled.
    const next = screen.getByRole('button', { name: 'Next' })
    expect(next).toBeDisabled()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Okta' } })
    expect(next).toBeEnabled()
  })

  it('walks all steps and submits the assembled form', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<IdpWizard open onClose={() => {}} onSubmit={onSubmit} />)

    // Step 1 — Basic Info
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Okta' } })
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))

    // Step 2 — SSO Endpoints
    fireEvent.change(screen.getByLabelText('SSO URL'), {
      target: { value: 'https://idp.example.com/sso' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))

    // Step 3 — Certificate + Entity ID
    fireEvent.change(screen.getByLabelText('Entity ID'), {
      target: { value: 'https://idp.example.com/saml' },
    })
    fireEvent.change(screen.getByPlaceholderText(/BEGIN CERTIFICATE/), {
      target: { value: '-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))

    // Step 4 — Attribute & Role Mapping (optional) → Next
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))

    // Step 5 — Review & Save
    fireEvent.click(screen.getByRole('button', { name: /Save & Activate/ }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce())
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Okta',
        sso_url: 'https://idp.example.com/sso',
        entity_id: 'https://idp.example.com/saml',
      }),
    )
  })
})
