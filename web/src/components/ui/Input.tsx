import { forwardRef } from 'react'
import { clsx } from 'clsx'

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ label, error, className, id, ...props }, ref) => {
    const inputId = id ?? label?.toLowerCase().replace(/\s+/g, '-')
    return (
      <div className="flex flex-col gap-1">
        {label && (
          <label htmlFor={inputId} className="text-sm font-medium text-gray-700 dark:text-gray-300">
            {label}
          </label>
        )}
        <input
          ref={ref}
          id={inputId}
          className={clsx(
            'h-9 rounded-md border bg-white px-3 text-sm text-gray-900 shadow-sm transition-colors',
            'focus:outline-none focus:ring-2 focus:ring-indigo-500',
            'dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400',
            error ? 'border-red-400 dark:border-red-500' : 'border-gray-300',
            className,
          )}
          {...props}
        />
        {error && <span className="text-xs text-red-500">{error}</span>}
      </div>
    )
  },
)
Input.displayName = 'Input'
