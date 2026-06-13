import { useState } from 'react'
import type { SAMLIdP } from '../../api/types'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { Modal } from '../ui/Modal'
import { KeyValueEditor } from '../ui/KeyValueEditor'

export interface IdpWizardForm {
  name: string
  is_enabled: boolean
  sso_url: string
  slo_url: string
  certificate: string
  entity_id: string
  attribute_map: Record<string, string>
  group_role_map: Record<string, string>
}

const defaultForm = (): IdpWizardForm => ({
  name: '',
  is_enabled: true,
  sso_url: '',
  slo_url: '',
  certificate: '',
  entity_id: '',
  attribute_map: {},
  group_role_map: {},
})

const TOTAL_STEPS = 5

interface Props {
  open: boolean
  onClose: () => void
  onSubmit: (form: Partial<SAMLIdP>) => Promise<unknown>
  submitting?: boolean
}

// IdpWizard is a 5-step guided setup for a new SAML IdP. Each step validates its
// own fields before Next is allowed. The final step submits the assembled form.
// Editing an existing IdP uses the flat modal in IdpListPage, not this wizard.
export function IdpWizard({ open, onClose, onSubmit, submitting }: Props) {
  const [step, setStep] = useState(1)
  const [form, setForm] = useState<IdpWizardForm>(defaultForm())
  const [error, setError] = useState<string | null>(null)

  function reset() {
    setStep(1)
    setForm(defaultForm())
    setError(null)
  }

  function close() {
    reset()
    onClose()
  }

  // stepValid reports whether the current step's required fields are filled.
  function stepValid(): boolean {
    switch (step) {
      case 1:
        return form.name.trim() !== ''
      case 2:
        return form.sso_url.trim() !== ''
      case 3:
        return form.certificate.trim() !== '' && form.entity_id.trim() !== ''
      default:
        return true
    }
  }

  function next() {
    if (!stepValid()) {
      setError('Please complete the required fields.')
      return
    }
    setError(null)
    setStep((s) => Math.min(TOTAL_STEPS, s + 1))
  }

  function back() {
    setError(null)
    setStep((s) => Math.max(1, s - 1))
  }

  async function save() {
    setError(null)
    try {
      await onSubmit({
        name: form.name,
        is_enabled: form.is_enabled,
        sso_url: form.sso_url,
        slo_url: form.slo_url.trim() || null,
        certificate: form.certificate,
        entity_id: form.entity_id,
        attribute_map: form.attribute_map,
        group_role_map: form.group_role_map,
      })
      close()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed')
    }
  }

  const titles = [
    'Basic Info',
    'SSO Endpoints',
    'Identity Provider Certificate',
    'Attribute & Role Mapping',
    'Review & Save',
  ]

  return (
    <Modal open={open} onClose={close} title={`Setup IdP — ${titles[step - 1]}`} className="max-w-2xl">
      {/* Progress indicator */}
      <div className="mb-6 flex gap-1">
        {Array.from({ length: TOTAL_STEPS }, (_, i) => (
          <div
            key={i}
            className={`h-1 flex-1 rounded ${i + 1 <= step ? 'bg-indigo-600' : 'bg-gray-200 dark:bg-gray-700'}`}
          />
        ))}
      </div>

      <div className="space-y-4">
        {step === 1 && (
          <>
            <Input
              label="Name"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              required
            />
            <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                type="checkbox"
                checked={form.is_enabled}
                onChange={(e) => setForm((f) => ({ ...f, is_enabled: e.target.checked }))}
              />
              Enabled
            </label>
          </>
        )}

        {step === 2 && (
          <>
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
          </>
        )}

        {step === 3 && (
          <>
            <Input
              label="Entity ID"
              placeholder="https://idp.example.com/saml"
              value={form.entity_id}
              onChange={(e) => setForm((f) => ({ ...f, entity_id: e.target.value }))}
              required
            />
            <div className="flex flex-col gap-1">
              <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Certificate (PEM)
              </label>
              <textarea
                rows={8}
                className="rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                placeholder="-----BEGIN CERTIFICATE-----&#10;…&#10;-----END CERTIFICATE-----"
                value={form.certificate}
                onChange={(e) => setForm((f) => ({ ...f, certificate: e.target.value }))}
              />
            </div>
          </>
        )}

        {step === 4 && (
          <>
            <KeyValueEditor
              label="Attribute mapping"
              value={form.attribute_map}
              onChange={(m) => setForm((f) => ({ ...f, attribute_map: m }))}
              keyPlaceholder="SAML attribute (e.g. urn:oid:0.9.2342…)"
              valuePlaceholder="mcpgate field (e.g. email)"
            />
            <KeyValueEditor
              label="Group → role mapping"
              value={form.group_role_map}
              onChange={(m) => setForm((f) => ({ ...f, group_role_map: m }))}
              keyPlaceholder="IdP group name"
              valuePlaceholder="mcpgate role (e.g. admin)"
            />
          </>
        )}

        {step === 5 && (
          <div className="space-y-2 text-sm">
            <ReviewRow label="Name" value={form.name} />
            <ReviewRow label="Enabled" value={form.is_enabled ? 'yes' : 'no'} />
            <ReviewRow label="SSO URL" value={form.sso_url} />
            <ReviewRow label="SLO URL" value={form.slo_url || '—'} />
            <ReviewRow label="Entity ID" value={form.entity_id} />
            <ReviewRow label="Certificate" value={form.certificate ? 'provided' : '—'} />
            <ReviewRow label="Attribute map" value={`${Object.keys(form.attribute_map).length} entries`} />
            <ReviewRow label="Group→role map" value={`${Object.keys(form.group_role_map).length} entries`} />
            <a href="/auth/saml/metadata" download="sp-metadata.xml" target="_blank" rel="noreferrer">
              <Button type="button" variant="secondary" className="mt-2">
                Download SP metadata XML
              </Button>
            </a>
          </div>
        )}

        {error && <p className="text-xs text-red-500">{error}</p>}

        <div className="flex justify-between pt-2">
          <Button type="button" variant="ghost" onClick={close}>
            Cancel
          </Button>
          <div className="flex gap-2">
            {step > 1 && (
              <Button type="button" variant="secondary" onClick={back}>
                Back
              </Button>
            )}
            {step < TOTAL_STEPS ? (
              <Button type="button" onClick={next} disabled={!stepValid()}>
                Next
              </Button>
            ) : (
              <Button type="button" onClick={save} loading={submitting}>
                Save &amp; Activate
              </Button>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}

function ReviewRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-4 border-b border-gray-100 py-1 dark:border-gray-700">
      <span className="text-gray-500 dark:text-gray-400">{label}</span>
      <span className="truncate text-gray-900 dark:text-gray-100">{value}</span>
    </div>
  )
}
