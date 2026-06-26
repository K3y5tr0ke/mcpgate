import { useState } from 'react'
import { Button } from './Button'

interface Props {
  label: string
  value: Record<string, string>
  onChange: (next: Record<string, string>) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
}

const cellClasses =
  'h-9 flex-1 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 ' +
  'focus:outline-none focus:ring-2 focus:ring-indigo-500 ' +
  'dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400'

// KeyValueEditor is a controlled editor for a Record<string,string>. The parent
// owns the canonical value; this component holds transient state only for the
// not-yet-added row. Used for SAML attribute_map and group_role_map.
export function KeyValueEditor({
  label,
  value,
  onChange,
  keyPlaceholder = 'key',
  valuePlaceholder = 'value',
}: Props) {
  const [newKey, setNewKey] = useState('')
  const [newValue, setNewValue] = useState('')

  const entries = Object.entries(value)

  function updateKey(oldKey: string, nextKey: string) {
    if (nextKey === oldKey) return
    const next: Record<string, string> = {}
    for (const [k, v] of Object.entries(value)) {
      next[k === oldKey ? nextKey : k] = v
    }
    onChange(next)
  }

  function updateValue(key: string, nextValue: string) {
    onChange({ ...value, [key]: nextValue })
  }

  function removePair(key: string) {
    const next = { ...value }
    delete next[key]
    onChange(next)
  }

  function addPair() {
    const k = newKey.trim()
    if (!k) return
    onChange({ ...value, [k]: newValue })
    setNewKey('')
    setNewValue('')
  }

  return (
    <div className="flex flex-col gap-2">
      <label className="text-sm font-medium text-gray-700 dark:text-gray-300">{label}</label>

      {entries.length === 0 && (
        <p className="text-xs text-gray-400">No mappings yet.</p>
      )}

      {entries.map(([k, v]) => (
        <div key={k} className="flex items-center gap-2">
          <input
            className={cellClasses}
            value={k}
            aria-label={`${label} key`}
            onChange={(e) => updateKey(k, e.target.value)}
          />
          <span className="text-gray-400">→</span>
          <input
            className={cellClasses}
            value={v}
            aria-label={`${label} value`}
            onChange={(e) => updateValue(k, e.target.value)}
          />
          <Button type="button" size="sm" variant="ghost" onClick={() => removePair(k)} aria-label="Remove">
            ✕
          </Button>
        </div>
      ))}

      <div className="flex items-center gap-2 border-t border-gray-100 pt-2 dark:border-gray-700">
        <input
          className={cellClasses}
          placeholder={keyPlaceholder}
          value={newKey}
          aria-label={`${label} new key`}
          onChange={(e) => setNewKey(e.target.value)}
        />
        <span className="text-gray-400">→</span>
        <input
          className={cellClasses}
          placeholder={valuePlaceholder}
          value={newValue}
          aria-label={`${label} new value`}
          onChange={(e) => setNewValue(e.target.value)}
        />
        <Button type="button" size="sm" variant="secondary" onClick={addPair} disabled={!newKey.trim()}>
          Add
        </Button>
      </div>
    </div>
  )
}
