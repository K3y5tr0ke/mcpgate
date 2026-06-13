import { clsx } from 'clsx'

type BadgeColor = 'green' | 'red' | 'yellow' | 'gray' | 'blue' | 'purple'

interface BadgeProps {
  color?: BadgeColor
  children: React.ReactNode
  className?: string
}

const colorMap: Record<BadgeColor, string> = {
  green: 'bg-green-100 text-green-700',
  red: 'bg-red-100 text-red-700',
  yellow: 'bg-yellow-100 text-yellow-700',
  gray: 'bg-gray-100 text-gray-600',
  blue: 'bg-blue-100 text-blue-700',
  purple: 'bg-purple-100 text-purple-700',
}

export function Badge({ color = 'gray', children, className }: BadgeProps) {
  return (
    <span
      className={clsx(
        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
        colorMap[color],
        className,
      )}
    >
      {children}
    </span>
  )
}
