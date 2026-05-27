import { clsx } from 'clsx'

interface CardProps {
  title?: string
  footer?: React.ReactNode
  children: React.ReactNode
  className?: string
}

export function Card({ title, footer, children, className }: CardProps) {
  return (
    <div className={clsx('rounded-lg border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800', className)}>
      {title && (
        <div className="border-b border-gray-100 px-4 py-3 dark:border-gray-700">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white">{title}</h3>
        </div>
      )}
      <div className="p-4">{children}</div>
      {footer && (
        <div className="border-t border-gray-100 px-4 py-3 text-sm text-gray-500 dark:border-gray-700 dark:text-gray-400">{footer}</div>
      )}
    </div>
  )
}
