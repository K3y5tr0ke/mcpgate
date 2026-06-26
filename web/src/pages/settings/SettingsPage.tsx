import { useState } from 'react'
import { useSettings, useUpdateSettings } from '../../api/queries'
import type { Settings } from '../../api/types'
import { AppShell } from '../../components/layout/AppShell'
import { Card } from '../../components/ui/Card'
import { Button } from '../../components/ui/Button'

const SETTING_META: Record<string, { label: string; description: string; type: 'number' | 'boolean' | 'string' }> = {
  session_ttl_seconds: {
    label: 'Session TTL (seconds)',
    description: 'How long a session token remains valid after issuance.',
    type: 'number',
  },
  rate_limit_default_rpm: {
    label: 'Default rate limit (req/min)',
    description: 'Per-user request rate limit in requests per minute. 0 = unlimited.',
    type: 'number',
  },
  rate_limit_default_rpd: {
    label: 'Default rate limit (req/day)',
    description: 'Per-user request rate limit in requests per day. 0 = unlimited.',
    type: 'number',
  },
  audit_log_retention_days: {
    label: 'Audit log retention (days)',
    description: 'How many days audit log entries are retained before deletion. 0 = keep forever.',
    type: 'number',
  },
  allow_public_metadata: {
    label: 'Allow public metadata endpoint',
    description: 'Expose SAML SP metadata at /auth/saml/metadata without authentication.',
    type: 'boolean',
  },
}

export function SettingsPage() {
  const { data: settings, isLoading } = useSettings()
  const updateSettings = useUpdateSettings()

  const [draft, setDraft] = useState<Partial<Settings>>({})
  const [saved, setSaved] = useState(false)

  // Seed the editable draft from fetched settings without an effect. Re-syncs
  // whenever the settings object identity changes (i.e. after a fresh load),
  // matching the previous [settings]-dependency behaviour.
  const [syncedFrom, setSyncedFrom] = useState<Settings | undefined>(undefined)
  if (settings && settings !== syncedFrom) {
    setSyncedFrom(settings)
    setDraft({ ...settings })
  }

  async function handleSave(e: React.FormEvent) {
    e.preventDefault()
    await updateSettings.mutateAsync(draft)
    setSaved(true)
    setTimeout(() => setSaved(false), 2000)
  }

  function setValue(key: string, raw: string, type: 'number' | 'boolean' | 'string') {
    const value =
      type === 'number' ? Number(raw) : type === 'boolean' ? raw === 'true' : raw
    setDraft((d) => ({ ...d, [key]: value }))
  }

  const knownKeys = Object.keys(SETTING_META)
  const extraKeys = settings ? Object.keys(settings).filter((k) => !knownKeys.includes(k)) : []

  return (
    <AppShell>
      <div className="space-y-6">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Settings</h1>

        {isLoading ? (
          <p className="text-sm text-gray-400">Loading…</p>
        ) : (
          <form onSubmit={handleSave} className="space-y-4">
            <Card title="General">
              <div className="divide-y divide-gray-50 dark:divide-gray-700">
                {knownKeys.map((key) => {
                  const meta = SETTING_META[key]
                  const val = draft[key] ?? settings?.[key]
                  return (
                    <SettingRow
                      key={key}
                      label={meta.label}
                      description={meta.description}
                    >
                      {meta.type === 'boolean' ? (
                        <select
                          value={String(val ?? false)}
                          onChange={(e) => setValue(key, e.target.value, 'boolean')}
                          className="h-9 w-24 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                        >
                          <option value="true">Enabled</option>
                          <option value="false">Disabled</option>
                        </select>
                      ) : (
                        <input
                          type="number"
                          value={String(val ?? 0)}
                          min={0}
                          onChange={(e) => setValue(key, e.target.value, 'number')}
                          className="h-9 w-28 rounded-md border border-gray-300 bg-white px-3 text-sm tabular-nums text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                        />
                      )}
                    </SettingRow>
                  )
                })}
              </div>
            </Card>

            {extraKeys.length > 0 && (
              <Card title="Other settings">
                <div className="divide-y divide-gray-50 dark:divide-gray-700">
                  {extraKeys.map((key) => {
                    const val = draft[key] ?? settings?.[key]
                    return (
                      <SettingRow key={key} label={key} description="">
                        <input
                          value={String(val ?? '')}
                          onChange={(e) => setValue(key, e.target.value, 'string')}
                          className="h-9 w-40 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                        />
                      </SettingRow>
                    )
                  })}
                </div>
              </Card>
            )}

            <div className="flex items-center gap-3">
              <Button type="submit" loading={updateSettings.isPending}>
                Save settings
              </Button>
              {saved && <span className="text-sm text-green-600">Saved</span>}
            </div>
          </form>
        )}
      </div>
    </AppShell>
  )
}

function SettingRow({
  label,
  description,
  children,
}: {
  label: string
  description: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between py-3">
      <div className="pr-4">
        <p className="text-sm font-medium text-gray-900 dark:text-white">{label}</p>
        {description && <p className="text-xs text-gray-400">{description}</p>}
      </div>
      {children}
    </div>
  )
}
