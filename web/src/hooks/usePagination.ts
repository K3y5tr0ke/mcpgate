import { useCallback, useState } from 'react'

export function usePagination(defaultPageSize = 20) {
  const [page, setPage] = useState(1)
  const [pageSize] = useState(defaultPageSize)

  const goToPage = useCallback((n: number) => setPage(n), [])
  const reset = useCallback(() => setPage(1), [])

  return { page, pageSize, goToPage, reset }
}
