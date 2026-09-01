export type Protocol = 'openvpn_udp' | 'openvpn_tcp' | 'softether_https' | 'sstp' | 'l2tp'

export interface Node {
  hostName: string
  ip: string
  score: number
  pingMs: number
  speedBps: number
  countryLong: string
  countryShort: string
  sessions: number
  operator: string
  message: string
  protocols: Protocol[]
  measuredBps: number
  measuredAt?: string
  speedTestFailed: boolean
  lastFailure?: string
  failureReason?: string
}

export interface Status {
  state: string
  selection: 'auto' | 'manual'
  activeIp?: string
  activeHostName?: string
  activeProtocol?: Protocol
  lastRefresh?: string
  lastHealthCheck?: string
  lastError?: string
  connectingIp?: string
  refreshRunning: boolean
}

export interface VersionInfo {
  version: string
  release: string
  commit: string
  channel: string
  toolchain?: string
  buildTime?: string
  goVersion: string
  os: string
  arch: string
}

export interface Config {
  sourceUrl: string
  refreshInterval: string
  filterExpression: string
  selectionMode: 'speed' | 'ping' | 'score'
  followRankingOnRefresh: boolean
  socks5: { listenAddress: string; username: string; password: string }
  monitor: { url: string; interval: string; timeout: string }
  web: { listenAddress: string; username: string; passwordHash: string; sessionSecret: string }
  databasePath: string
  dnsServers: string[]
  speedTestUrl: string
  speedTestTimeout: string
  connectTimeout: string
  protocolPriority: Protocol[]
  vpnGateUsername: string
  vpnGatePassword: string
  vpnGatePreSharedKey: string
}
