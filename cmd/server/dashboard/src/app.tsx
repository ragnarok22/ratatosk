import { useEffect, useState } from 'react'

interface Tunnel {
  subdomain: string
  connected_at: string
}

interface TunnelsResponse {
  tunnels: Tunnel[]
}

interface VersionInfo {
  version: string
  latest_version?: string
  update_available: boolean
}

function formatUptime(connectedAt: string): string {
  const diff = Date.now() - new Date(connectedAt).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const secs = seconds % 60
  if (minutes < 60) return `${minutes}m ${secs}s`
  const hours = Math.floor(minutes / 60)
  const mins = minutes % 60
  return `${hours}h ${mins}m`
}

function App() {
  const [tunnels, setTunnels] = useState<Tunnel[]>([])
  const [versionInfo, setVersionInfo] = useState<VersionInfo | null>(null)

  useEffect(() => {
    const fetchTunnels = async () => {
      try {
        const res = await fetch('/api/tunnels')
        const data: TunnelsResponse = await res.json()
        setTunnels(data.tunnels ?? [])
      } catch {
        // Server unavailable — keep stale data.
      }
    }

    fetchTunnels()
    const id = setInterval(fetchTunnels, 3000)
    return () => clearInterval(id)
  }, [])

  useEffect(() => {
    fetch('/api/version')
      .then((res) => res.json())
      .then((data: VersionInfo) => setVersionInfo(data))
      .catch(() => {})
  }, [])

  return (
    <div className="min-h-screen bg-[#0f1117] text-[#c9d1d9]">
      <header className="flex items-center gap-3 border-b border-[#30363d] bg-[#161b22] px-6 py-3.5">
        <h1 className="text-base font-semibold text-[#e6edf3]">
          Ratatosk Admin
        </h1>
        {versionInfo && (
          <span className="text-xs text-[#8b949e]">{versionInfo.version}</span>
        )}
        <span className="rounded-full bg-[#30363d] px-2.5 py-0.5 text-xs font-semibold text-[#c9d1d9]">
          {tunnels.length} tunnel{tunnels.length !== 1 ? 's' : ''}
        </span>
      </header>

      {versionInfo?.update_available && (
        <div className="border-b border-[#9e6a03] bg-[#1c1305] px-6 py-2.5 text-sm text-[#e3b341]">
          A new version of Ratatosk is available ({versionInfo.latest_version}).
        </div>
      )}

      <main className="p-6">
        {tunnels.length === 0 ? (
          <p className="py-16 text-center text-[#8b949e]">No active tunnels</p>
        ) : (
          <div className="overflow-hidden rounded-lg border border-[#30363d]">
            <table className="w-full border-collapse">
              <thead>
                <tr className="bg-[#161b22]">
                  <th className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]">
                    Subdomain
                  </th>
                  <th className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]">
                    URL
                  </th>
                  <th className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]">
                    Connected Since
                  </th>
                  <th className="border-b border-[#30363d] px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wider text-[#8b949e]">
                    Uptime
                  </th>
                </tr>
              </thead>
              <tbody>
                {tunnels.map((t) => (
                  <tr
                    key={t.subdomain}
                    className="border-b border-[#21262d] transition-colors hover:bg-[#1c2128]"
                  >
                    <td className="px-4 py-2.5 font-mono text-sm">
                      {t.subdomain}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-sm text-[#58a6ff]">
                      {t.subdomain}.localhost:8080
                    </td>
                    <td className="px-4 py-2.5 text-sm text-[#8b949e]">
                      {new Date(t.connected_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-2.5 text-sm text-[#8b949e]">
                      {formatUptime(t.connected_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </main>
    </div>
  )
}

export default App
