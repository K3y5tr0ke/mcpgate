import { describe, it, expect } from 'vitest'

// ---- api/client toQS --------------------------------------------------------

import { toQS } from '../api/client'

describe('toQS', () => {
  it('serialises defined values', () => {
    const qs = toQS({ page: 2, search: 'foo', empty: '' })
    expect(qs).toContain('page=2')
    expect(qs).toContain('search=foo')
    // empty string should be omitted
    expect(qs).not.toContain('empty')
  })

  it('omits undefined values', () => {
    const qs = toQS({ a: undefined, b: 'x' })
    expect(qs).not.toContain('a')
    expect(qs).toContain('b=x')
  })

  it('handles boolean values', () => {
    const qs = toQS({ enabled: true, disabled: false })
    expect(qs).toContain('enabled=true')
    expect(qs).toContain('disabled=false')
  })
})

// ---- ServersListPage formToConfig -------------------------------------------
// Validate that the transport-config builder produces the right shapes.

// Replicate the function here rather than importing the page (avoids router deps).
type Transport = 'streamable_http' | 'sse' | 'stdio'
interface FormState {
  transport: Transport
  endpoint: string
  sseEndpoint: string
  messageEndpoint: string
  command: string
  args: string
}

function formToConfig(f: FormState): Record<string, unknown> {
  switch (f.transport) {
    case 'streamable_http':
      return { endpoint: f.endpoint.trim() }
    case 'sse':
      return { sse_endpoint: f.sseEndpoint.trim(), message_endpoint: f.messageEndpoint.trim() }
    case 'stdio': {
      const args = f.args.trim() ? f.args.trim().split(/\s+/) : []
      return { command: f.command.trim(), ...(args.length ? { args } : {}) }
    }
  }
}

describe('formToConfig', () => {
  it('builds streamable_http config', () => {
    const cfg = formToConfig({
      transport: 'streamable_http',
      endpoint: ' https://example.com/mcp ',
      sseEndpoint: '', messageEndpoint: '', command: '', args: '',
    })
    expect(cfg).toEqual({ endpoint: 'https://example.com/mcp' })
  })

  it('builds sse config', () => {
    const cfg = formToConfig({
      transport: 'sse',
      sseEndpoint: 'https://example.com/sse',
      messageEndpoint: 'https://example.com/msg',
      endpoint: '', command: '', args: '',
    })
    expect(cfg).toEqual({
      sse_endpoint: 'https://example.com/sse',
      message_endpoint: 'https://example.com/msg',
    })
  })

  it('builds stdio config without args', () => {
    const cfg = formToConfig({
      transport: 'stdio',
      command: 'node',
      args: '',
      endpoint: '', sseEndpoint: '', messageEndpoint: '',
    })
    expect(cfg).toEqual({ command: 'node' })
  })

  it('builds stdio config with args', () => {
    const cfg = formToConfig({
      transport: 'stdio',
      command: 'npx',
      args: '-y @mcp/server /tmp',
      endpoint: '', sseEndpoint: '', messageEndpoint: '',
    })
    expect(cfg).toEqual({ command: 'npx', args: ['-y', '@mcp/server', '/tmp'] })
  })
})

// ---- statusLabel (mirrors Go's statusLabel) ----------------------------------

function statusLabel(code: number): string {
  if (code >= 200 && code < 300) return '2xx'
  if (code >= 300 && code < 400) return '3xx'
  if (code >= 400 && code < 500) return '4xx'
  if (code >= 500) return '5xx'
  return 'unknown'
}

describe('statusLabel', () => {
  it.each([
    [200, '2xx'], [201, '2xx'], [299, '2xx'],
    [301, '3xx'], [304, '3xx'],
    [400, '4xx'], [401, '4xx'], [404, '4xx'],
    [500, '5xx'], [503, '5xx'],
    [0, 'unknown'], [99, 'unknown'],
  ])('statusLabel(%i) === %s', (code, expected) => {
    expect(statusLabel(code)).toBe(expected)
  })
})
