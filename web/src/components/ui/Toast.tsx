import { useCallback, useState } from 'react'
import { clsx } from 'clsx'
import { ToastContext, type ToastContextValue } from './toast-context'

interface ToastItem {
  id: number
  type: 'success' | 'error' | 'info'
  message: string
}

let nextId = 0

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])

  const add = useCallback((type: ToastItem['type'], message: string) => {
    const id = ++nextId
    setToasts((prev) => [...prev, { id, type, message }])
    setTimeout(() => setToasts((prev) => prev.filter((t) => t.id !== id)), 4000)
  }, [])

  const value: ToastContextValue = {
    success: (msg) => add('success', msg),
    error: (msg) => add('error', msg),
    info: (msg) => add('info', msg),
  }

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={clsx(
              'pointer-events-auto rounded-lg px-4 py-3 text-sm text-white shadow-lg',
              t.type === 'success' && 'bg-green-600',
              t.type === 'error' && 'bg-red-600',
              t.type === 'info' && 'bg-gray-700',
            )}
          >
            {t.message}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

// Convenience wrapper that mounts the provider.
export function Toast({ children }: { children: React.ReactNode }) {
  return <ToastProvider>{children}</ToastProvider>
}
