import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './app'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('dashboard', () => {
  it('renders HTTP, TCP, and UDP tunnels with their canonical endpoints', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input)
        if (path === '/api/version') {
          return Response.json({
            version: 'v1.2.3',
            update_available: false,
          })
        }
        return Response.json({
          tunnels: [
            {
              protocol: 'http',
              subdomain: 'brisk-oak',
              endpoint: 'https://brisk-oak.tunnels.example.test',
              connected_at: '2026-07-11T12:00:00Z',
            },
            {
              protocol: 'tcp',
              public_port: 12000,
              endpoint: 'tunnels.example.test:12000',
              connected_at: '2026-07-11T12:01:00Z',
            },
            {
              protocol: 'udp',
              public_port: 12001,
              endpoint: 'tunnels.example.test:12001',
              connected_at: '2026-07-11T12:02:00Z',
            },
          ],
        })
      }),
    )

    render(<App />)

    expect(
      await screen.findByRole('row', { name: /http brisk-oak/ }),
    ).toHaveTextContent('https://brisk-oak.tunnels.example.test')
    expect(screen.getByRole('row', { name: /tcp 12000/ })).toHaveTextContent(
      'tunnels.example.test:12000',
    )
    expect(screen.getByRole('row', { name: /udp 12001/ })).toHaveTextContent(
      'tunnels.example.test:12001',
    )
  })

  it('shows an explicit error when the initial tunnel fetch fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input) === '/api/version') {
          return Response.json({
            version: 'dev',
            update_available: false,
          })
        }
        return new Response('unavailable', { status: 503 })
      }),
    )

    render(<App />)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Unable to load tunnels',
    )
  })

  it('aborts a hung tunnel request and continues with an error state', async () => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === '/api/version') {
          return Promise.resolve(
            Response.json({ version: 'dev', update_available: false }),
          )
        }
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => {
            reject(new DOMException('aborted', 'AbortError'))
          })
        })
      }),
    )

    render(<App />)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000)
    })

    expect(screen.getByRole('alert')).toHaveTextContent(
      'Unable to load tunnels',
    )
  })
})
