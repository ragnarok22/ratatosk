import { useEffect, useState } from 'react'

const pollIntervalMs = 3000
const requestTimeoutMs = 10000

interface TunnelBase {
  endpoint: string
  connected_at: string
}

interface HttpTunnel extends TunnelBase {
  protocol: 'http'
  subdomain: string
}

interface PortTunnel extends TunnelBase {
  protocol: 'tcp' | 'udp'
  public_port: number
}

type Tunnel = HttpTunnel | PortTunnel

interface VersionInfo {
  version: string
  latest_version?: string
  update_available: boolean
}

interface TunnelsState {
  tunnels: Tunnel[] | null
  failed: boolean
  stale: boolean
  lastUpdated: Date | null
}

type VersionState =
  | { status: 'loading' }
  | { status: 'loaded'; data: VersionInfo }
  | { status: 'error' }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function parseTunnel(value: unknown): Tunnel {
  if (
    !isRecord(value) ||
    typeof value.endpoint !== 'string' ||
    value.endpoint.length === 0 ||
    typeof value.connected_at !== 'string' ||
    !Number.isFinite(Date.parse(value.connected_at))
  ) {
    throw new Error('Invalid tunnel response')
  }
  if (
    value.protocol === 'http' &&
    typeof value.subdomain === 'string' &&
    value.subdomain.length > 0
  ) {
    return {
      protocol: value.protocol,
      subdomain: value.subdomain,
      endpoint: value.endpoint,
      connected_at: value.connected_at,
    }
  }
  if (
    (value.protocol === 'tcp' || value.protocol === 'udp') &&
    Number.isInteger(value.public_port) &&
    Number(value.public_port) > 0 &&
    Number(value.public_port) <= 65535
  ) {
    return {
      protocol: value.protocol,
      public_port: Number(value.public_port),
      endpoint: value.endpoint,
      connected_at: value.connected_at,
    }
  }
  throw new Error('Invalid tunnel response')
}

function parseTunnelsResponse(value: unknown): Tunnel[] {
  if (!isRecord(value) || !Array.isArray(value.tunnels)) {
    throw new Error('Invalid tunnels response')
  }
  return value.tunnels.map(parseTunnel)
}

function parseVersionResponse(value: unknown): VersionInfo {
  if (
    !isRecord(value) ||
    typeof value.version !== 'string' ||
    typeof value.update_available !== 'boolean' ||
    (value.latest_version !== undefined &&
      typeof value.latest_version !== 'string') ||
    (value.update_available && typeof value.latest_version !== 'string')
  ) {
    throw new Error('Invalid version response')
  }
  return {
    version: value.version,
    latest_version: value.latest_version,
    update_available: value.update_available,
  }
}

async function fetchJSON(path: string, signal: AbortSignal): Promise<unknown> {
  const response = await fetch(path, { signal })
  if (!response.ok) {
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

function formatUptime(connectedAt: string): string {
  const diff = Math.max(0, Date.now() - new Date(connectedAt).getTime())
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const secs = seconds % 60
  if (minutes < 60) return `${minutes}m ${secs}s`
  const hours = Math.floor(minutes / 60)
  const mins = minutes % 60
  return `${hours}h ${mins}m`
}

function tunnelKey(tunnel: Tunnel): string {
  return tunnel.protocol === 'http'
    ? `http:${tunnel.subdomain}`
    : `${tunnel.protocol}:${tunnel.public_port}`
}

function tunnelName(tunnel: Tunnel): string {
  return tunnel.protocol === 'http'
    ? tunnel.subdomain
    : String(tunnel.public_port)
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function App() {
  const [tunnelsState, setTunnelsState] = useState<TunnelsState>({
    tunnels: null,
    failed: false,
    stale: false,
    lastUpdated: null,
  })
  const [versionState, setVersionState] = useState<VersionState>({
    status: 'loading',
  })

  useEffect(() => {
    let stopped = false
    let timeoutId: number | undefined
    let controller: AbortController | null = null
    let fetchAgainWhenSettled = false
    let requestTimedOut = false

    const schedule = () => {
      if (!stopped && !document.hidden) {
        timeoutId = window.setTimeout(poll, pollIntervalMs)
      }
    }

    const poll = async () => {
      if (stopped || document.hidden || controller !== null) return

      controller = new AbortController()
      requestTimedOut = false
      const requestTimeoutId = window.setTimeout(() => {
        requestTimedOut = true
        controller?.abort()
      }, requestTimeoutMs)
      try {
        const payload = await fetchJSON('/api/tunnels', controller.signal)
        const tunnels = parseTunnelsResponse(payload)
        if (!stopped) {
          setTunnelsState({
            tunnels,
            failed: false,
            stale: false,
            lastUpdated: new Date(),
          })
        }
      } catch (error) {
        if (!stopped && (requestTimedOut || !isAbortError(error))) {
          setTunnelsState((current) => ({
            ...current,
            failed: true,
            stale: current.tunnels !== null,
          }))
        }
      } finally {
        window.clearTimeout(requestTimeoutId)
        controller = null
        if (!stopped) {
          if (fetchAgainWhenSettled && !document.hidden) {
            fetchAgainWhenSettled = false
            void poll()
          } else {
            schedule()
          }
        }
      }
    }

    const handleVisibilityChange = () => {
      if (document.hidden) {
        if (timeoutId !== undefined) window.clearTimeout(timeoutId)
        controller?.abort()
        return
      }
      if (controller !== null) {
        fetchAgainWhenSettled = true
      } else {
        void poll()
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    void poll()

    return () => {
      stopped = true
      if (timeoutId !== undefined) window.clearTimeout(timeoutId)
      controller?.abort()
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()

    fetchJSON('/api/version', controller.signal)
      .then(parseVersionResponse)
      .then((data) => {
        if (!controller.signal.aborted) {
          setVersionState({ status: 'loaded', data })
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted && !isAbortError(error)) {
          setVersionState({ status: 'error' })
        }
      })

    return () => controller.abort()
  }, [])

  const tunnels = tunnelsState.tunnels ?? []
  const isLoading = tunnelsState.tunnels === null && !tunnelsState.failed
  const versionInfo =
    versionState.status === 'loaded' ? versionState.data : null
  const statusLabel = isLoading
    ? 'Connecting'
    : tunnelsState.stale
      ? 'Status stale'
      : tunnelsState.failed
        ? 'Unavailable'
        : 'Live'
  const statusColor = tunnelsState.stale
    ? 'text-[#e3b341]'
    : tunnelsState.failed
      ? 'text-[#f85149]'
      : 'text-[#3fb950]'

  return (
    <div className="min-h-screen bg-[#0f1117] text-[#c9d1d9]">
      <header className="flex flex-wrap items-center gap-3 border-b border-[#30363d] bg-[#161b22] px-4 py-3.5 sm:px-6">
        <h1 className="text-base font-semibold text-[#e6edf3]">
          Ratatosk Admin
        </h1>
        {versionInfo ? (
          <span className="text-xs text-[#8b949e]">{versionInfo.version}</span>
        ) : versionState.status === 'error' ? (
          <span className="text-xs text-[#8b949e]">Version unavailable</span>
        ) : null}
        <span
          className="rounded-full bg-[#30363d] px-2.5 py-0.5 text-xs font-semibold text-[#c9d1d9]"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {isLoading
            ? 'Loading tunnels'
            : `${tunnels.length} tunnel${tunnels.length === 1 ? '' : 's'}`}
        </span>
        <span className={`ml-auto text-xs font-medium ${statusColor}`}>
          {statusLabel}
        </span>
      </header>

      {versionInfo?.update_available ? (
        <div className="border-b border-[#9e6a03] bg-[#1c1305] px-4 py-2.5 text-sm text-[#e3b341] sm:px-6">
          A new version of Ratatosk is available ({versionInfo.latest_version}).
        </div>
      ) : null}

      <main className="p-4 sm:p-6">
        {tunnelsState.failed ? (
          <div
            className="mb-4 rounded-md border border-[#9e6a03] bg-[#1c1305] px-4 py-3 text-sm text-[#e3b341]"
            role="alert"
          >
            {tunnelsState.stale
              ? `Unable to refresh tunnels. Showing stale data from ${tunnelsState.lastUpdated?.toLocaleTimeString()}.`
              : 'Unable to load tunnels. Retrying automatically.'}
          </div>
        ) : null}

        {isLoading ? (
          <p className="py-16 text-center text-[#8b949e]">Loading tunnels...</p>
        ) : tunnelsState.tunnels === null ? null : tunnels.length === 0 ? (
          <p className="py-16 text-center text-[#8b949e]">No active tunnels</p>
        ) : (
          <section
            className="overflow-x-auto rounded-lg border border-[#30363d] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#58a6ff]"
            aria-label="Active tunnels"
          >
            <table className="min-w-[720px] w-full border-collapse">
              <caption className="sr-only">
                Active HTTP, TCP, and UDP tunnels
              </caption>
              <thead>
                <tr className="bg-[#161b22]">
                  <th
                    className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]"
                    scope="col"
                  >
                    Protocol
                  </th>
                  <th
                    className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]"
                    scope="col"
                  >
                    Tunnel
                  </th>
                  <th
                    className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]"
                    scope="col"
                  >
                    Endpoint
                  </th>
                  <th
                    className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]"
                    scope="col"
                  >
                    Connected Since
                  </th>
                  <th
                    className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]"
                    scope="col"
                  >
                    Uptime
                  </th>
                </tr>
              </thead>
              <tbody>
                {tunnels.map((tunnel) => (
                  <tr
                    key={tunnelKey(tunnel)}
                    className="border-b border-[#21262d] transition-colors last:border-b-0 hover:bg-[#1c2128]"
                  >
                    <th
                      className="px-4 py-2.5 text-left text-xs font-semibold uppercase text-[#c9d1d9]"
                      scope="row"
                    >
                      {tunnel.protocol}
                    </th>
                    <td className="px-4 py-2.5 font-mono text-sm">
                      {tunnelName(tunnel)}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-sm text-[#58a6ff]">
                      {tunnel.protocol === 'http' ? (
                        <a
                          className="underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#58a6ff]"
                          href={tunnel.endpoint}
                          rel="noreferrer"
                          target="_blank"
                        >
                          {tunnel.endpoint}
                        </a>
                      ) : (
                        tunnel.endpoint
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-sm text-[#8b949e]">
                      <time dateTime={tunnel.connected_at}>
                        {new Date(tunnel.connected_at).toLocaleString()}
                      </time>
                    </td>
                    <td className="px-4 py-2.5 text-sm text-[#8b949e]">
                      {formatUptime(tunnel.connected_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        )}
      </main>
    </div>
  )
}

export default App
