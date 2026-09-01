import type { TFunction } from 'i18next'
import type { ReactNode } from 'react'
import { FormEvent, memo, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { api } from './api'
import { localeConfig, resolvedLocale } from './i18n'
import type { Config, Node, Protocol, Status, VersionInfo } from './types'

const protocolLabels: Record<Protocol, string> = {
  openvpn_udp: 'OpenVPN UDP',
  openvpn_tcp: 'OpenVPN TCP',
  softether_https: 'SoftEther TLS',
  sstp: 'SSTP',
  l2tp: 'L2TP/IPsec',
}

type View = 'nodes' | 'settings'
type Theme = 'light' | 'dark'
type SortKey = 'speedBps' | 'pingMs' | 'score' | 'measuredBps' | 'countryShort'

const protocolDisplayOrder: Protocol[] = ['openvpn_udp', 'openvpn_tcp', 'softether_https', 'sstp', 'l2tp']
const selectionSort: Record<Config['selectionMode'], { key: SortKey; direction: 'asc' | 'desc' }> = {
  speed: { key: 'speedBps', direction: 'desc' },
  ping: { key: 'pingMs', direction: 'asc' },
  score: { key: 'score', direction: 'desc' },
}

function App() {
  const { t, i18n } = useTranslation()
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [version, setVersion] = useState<VersionInfo>()
  const [view, setView] = useState<View>('nodes')
  const [theme, setTheme] = useState<Theme>(preferredTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
    window.localStorage.setItem('proxygate:theme', theme)
  }, [theme])

  useEffect(() => {
    document.documentElement.lang = resolvedLocale(i18n.resolvedLanguage)
  }, [i18n.resolvedLanguage])

  useEffect(() => {
    api.get<VersionInfo>('/api/version').then(setVersion).catch(() => undefined)
    api.get<Status>('/api/status').then(() => setAuthenticated(true)).catch(() => setAuthenticated(false))
    const unauthorized = () => setAuthenticated(false)
    window.addEventListener('proxygate:unauthorized', unauthorized)
    return () => window.removeEventListener('proxygate:unauthorized', unauthorized)
  }, [])

  if (authenticated === null) return <LoadingScreen />
  if (!authenticated) return <Login version={version} theme={theme} onToggleTheme={() => setTheme(current => current === 'dark' ? 'light' : 'dark')} onSuccess={() => setAuthenticated(true)} />

  const logout = () => api.post('/api/logout').finally(() => setAuthenticated(false))
  return <div className="app-shell">
    <header className="topbar">
      <div className="brand">
        <div className="brand-mark" aria-hidden="true"><span /></div>
        <div><BrandTitle version={version} /><small>{t('app.subtitle')}</small></div>
      </div>
      <nav className="primary-nav" aria-label={t('app.navigation')}>
        <button className={view === 'nodes' ? 'active' : ''} onClick={() => setView('nodes')}><span>{t('app.nodes')}</span><small>{t('app.nodes')}</small></button>
        <button className={view === 'settings' ? 'active' : ''} onClick={() => setView('settings')}><span>{t('app.settings')}</span><small>{t('app.settings')}</small></button>
      </nav>
      <div className="topbar-actions">
        <LanguageSelect />
        <button className="quiet-button theme-button" title={t('theme.switch')} onClick={() => setTheme(current => current === 'dark' ? 'light' : 'dark')}><ThemeIcon theme={theme} /><span>{theme === 'dark' ? t('theme.light') : t('theme.dark')}</span></button>
        <button className="quiet-button" onClick={logout}>{t('app.logout')}</button>
      </div>
    </header>
    <main className="workspace">{view === 'nodes' ? <Nodes /> : <Settings />}</main>
  </div>
}

function LoadingScreen() {
  return <div className="loading-screen"><div className="brand-mark large" /><div className="pulse-line" /></div>
}

function Login({ version, theme, onToggleTheme, onSuccess }: { version?: VersionInfo; theme: Theme; onToggleTheme: () => void; onSuccess: () => void }) {
  const { t } = useTranslation()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError(''); setSubmitting(true)
    try {
      await api.post('/api/login', { username, password })
      onSuccess()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSubmitting(false)
    }
  }

  return <main className="login-page">
    <div className="login-controls"><LanguageSelect /><button className="quiet-button login-theme" title={t('theme.switch')} onClick={onToggleTheme}><ThemeIcon theme={theme} /><span>{theme === 'dark' ? t('theme.lightMode') : t('theme.darkMode')}</span></button></div>
    <section className="login-intro">
      <div className="brand login-brand"><div className="brand-mark large"><span /></div><div><BrandTitle version={version} /><small>{t('app.subtitle')}</small></div></div>
      <p className="eyebrow">PROXYGATE</p>
      <h1>{t('login.title')}</h1>
      <p className="login-copy">{t('login.description')}</p>
    </section>
    <form className="login-panel" onSubmit={submit}>
      <div><p className="eyebrow">{t('login.panel')}</p><h2>{t('login.heading')}</h2><p>{t('login.prompt')}</p></div>
      {error && <Notice tone="danger">{error}</Notice>}
      <Field label={t('login.username')}><input value={username} onChange={event => setUsername(event.target.value)} autoComplete="username" autoFocus /></Field>
      <Field label={t('login.password')}><input type="password" value={password} onChange={event => setPassword(event.target.value)} autoComplete="current-password" /></Field>
      <button className="primary-button wide" disabled={submitting}>{submitting ? <><Spinner />{t('login.checking')}</> : t('login.submit')}</button>
    </form>
  </main>
}

function BrandTitle({ version }: { version?: VersionInfo }) {
  return <strong>ProxyGate{version && <span className="app-version" title={version.release}>v{version.version}</span>}</strong>
}

function Nodes() {
  const { t, i18n } = useTranslation()
  const locale = resolvedLocale(i18n.resolvedLanguage)
  const sortLabels: Record<SortKey, string> = {
    speedBps: t('sort.speedBps'),
    measuredBps: t('sort.measuredBps'),
    pingMs: t('sort.pingMs'),
    score: t('sort.score'),
    countryShort: t('sort.countryShort'),
  }
  const [status, setStatus] = useState<Status>()
  const [nodes, setNodes] = useState<Node[]>([])
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [sortKey, setSortKey] = useState<SortKey>('speedBps')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [protocolPriority, setProtocolPriority] = useState<Protocol[]>(protocolDisplayOrder)
  const [page, setPage] = useState(1)
  const [loadingNodes, setLoadingNodes] = useState(true)
  const [reconnecting, setReconnecting] = useState(false)
  const [lastSynced, setLastSynced] = useState<Date>()
  const statusRequest = useRef(false)
  const nodesRequest = useRef(false)
  const nodesReloadPending = useRef(false)
  const refreshWasRunning = useRef(false)
  const previousActiveIP = useRef<string | undefined>(undefined)
  const sortTouched = useRef(false)
  const deferredQuery = useDeferredValue(query)
  const pageSize = 24

  const loadStatus = useCallback(async () => {
    if (statusRequest.current || document.visibilityState === 'hidden') return
    statusRequest.current = true
    try {
      setStatus(await api.get<Status>('/api/status'))
      setError('')
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      statusRequest.current = false
    }
  }, [])

  const loadNodes = useCallback(async () => {
    if (document.visibilityState === 'hidden') return
    if (nodesRequest.current) {
      nodesReloadPending.current = true
      return
    }
    nodesRequest.current = true
    try {
      do {
        nodesReloadPending.current = false
        setNodes(await api.get<Node[]>('/api/nodes?limit=240'))
        setLastSynced(new Date())
        setError('')
      } while (nodesReloadPending.current && !document.hidden)
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      nodesRequest.current = false
      setLoadingNodes(false)
    }
  }, [])

  useEffect(() => {
    void loadStatus(); void loadNodes()
    const statusTimer = window.setInterval(loadStatus, 4_000)
    const nodesTimer = window.setInterval(loadNodes, 60_000)
    const visible = () => { if (document.visibilityState === 'visible') { void loadStatus(); void loadNodes() } }
    document.addEventListener('visibilitychange', visible)
    return () => {
      window.clearInterval(statusTimer); window.clearInterval(nodesTimer)
      document.removeEventListener('visibilitychange', visible)
    }
  }, [loadNodes, loadStatus])

  useEffect(() => {
    let mounted = true
    void api.get<Config>('/api/config').then(settings => {
      if (!mounted) return
      setProtocolPriority(settings.protocolPriority)
      if (!sortTouched.current) {
        const preferred = selectionSort[settings.selectionMode]
        setSortKey(preferred.key)
        setSortDirection(preferred.direction)
      }
    }).catch(reason => {
      if (mounted) setError(errorMessage(reason))
    })
    return () => { mounted = false }
  }, [])

  useEffect(() => {
    if (refreshWasRunning.current && status?.refreshRunning === false) void loadNodes()
    refreshWasRunning.current = Boolean(status?.refreshRunning)
  }, [status?.refreshRunning, loadNodes])

  useEffect(() => {
    const activeIP = status?.activeIp
    if (!activeIP) return
    const previous = previousActiveIP.current
    previousActiveIP.current = activeIP
    if (previous && previous !== activeIP) void loadNodes()
  }, [status?.activeIp, loadNodes])

  useEffect(() => { setPage(1) }, [deferredQuery, sortKey, sortDirection, status?.activeIp])

  const filteredNodes = useMemo(() => {
    const normalized = deferredQuery.trim().toLowerCase()
    const result = normalized ? nodes.filter(node => [
      node.ip, node.hostName, node.operator, node.countryShort, node.countryLong, ...node.protocols,
    ].some(value => value.toLowerCase().includes(normalized))) : nodes
    const multiplier = sortDirection === 'asc' ? 1 : -1
    return [...result].sort((left, right) => {
      const leftActive = left.ip === status?.activeIp
      const rightActive = right.ip === status?.activeIp
      if (leftActive !== rightActive) return leftActive ? -1 : 1

      let comparison = 0
      switch (sortKey) {
        case 'speedBps': comparison = left.speedBps - right.speedBps; break
        case 'pingMs': comparison = normalizedPing(left.pingMs) - normalizedPing(right.pingMs); break
        case 'score': comparison = left.score - right.score; break
        case 'measuredBps': comparison = (left.measuredBps || 0) - (right.measuredBps || 0); break
        case 'countryShort': comparison = left.countryShort.localeCompare(right.countryShort, locale); break
      }
      return comparison * multiplier
    })
  }, [nodes, deferredQuery, sortKey, sortDirection, status?.activeIp, locale])

  const pageCount = Math.max(1, Math.ceil(filteredNodes.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const visibleNodes = filteredNodes.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const connected = status?.state === 'connected'

  const refresh = async () => {
    try {
      await api.post('/api/refresh')
      setStatus(current => current ? { ...current, refreshRunning: true } : current)
      await loadStatus()
    } catch (reason) {
      setError(errorMessage(reason))
    }
  }

  const reconnect = async () => {
    setError(''); setReconnecting(true)
    try {
      await api.post('/api/nodes/reconnect')
      setStatus(current => current ? { ...current, state: 'connecting', connectingIp: current.activeIp } : current)
      await loadStatus()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setReconnecting(false)
    }
  }

  return <div className="page-stack">
    <header className="page-heading">
      <div><p className="eyebrow">{t('nodes.eyebrow')}</p><h1>{t('nodes.heading')}</h1><p>{t('nodes.description')}</p></div>
      <div className="heading-actions">
        <span className="sync-label">{t('nodes.synced', { time: formatRelativeTime(lastSynced, t) })}</span>
        <button className="secondary-button" disabled={!status?.activeIp || status.state === 'connecting' || reconnecting} onClick={reconnect}>{reconnecting ? <Spinner /> : <RestartIcon />}{reconnecting ? t('nodes.reconnecting') : t('nodes.reconnectCurrent')}</button>
        <button className="primary-button" disabled={status?.refreshRunning} onClick={refresh}>{status?.refreshRunning ? <Spinner /> : <RefreshIcon />}{status?.refreshRunning ? t('nodes.refreshing') : t('nodes.refreshSource')}</button>
      </div>
    </header>

    {error && <Notice tone="danger">{error}</Notice>}
    {status?.lastError && <Notice tone="warning"><strong>{t('nodes.lastError')}</strong><span>{status.lastError}</span></Notice>}

    <section className="status-grid">
      <article className={`status-card connection ${connected ? 'healthy' : status?.state === 'connecting' ? 'pending' : 'unhealthy'}`}>
        <div className="status-card-head"><span>{t('status.connection')}</span><i /></div>
        <strong>{stateLabel(status?.state, t)}</strong>
        <p>{status?.selection === 'manual' ? t('status.manual') : t('status.automatic')}</p>
      </article>
      <article className="status-card"><div className="status-card-head"><span>{t('status.currentExit')}</span><small>{status?.activeProtocol ? protocolLabels[status.activeProtocol] : '—'}</small></div><strong className="mono compact">{status?.activeIp || t('status.notConnected')}</strong><p>{status?.activeHostName || t('status.waiting')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('status.healthCheck')}</span><small>{status?.lastError ? t('status.unhealthy') : t('status.healthy')}</small></div><strong className="compact">{formatRelativeTime(status?.lastHealthCheck, t)}</strong><p>{status?.lastHealthCheck ? formatDateTime(status.lastHealthCheck, locale) : t('status.notChecked')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('status.nodeCount')}</span><small>{t('status.filtered')}</small></div><strong>{nodes.length}</strong><p>{t('status.showing', { count: filteredNodes.length })}</p></article>
    </section>

    <section className="node-panel">
      <div className="node-toolbar">
        <label className="search-box"><SearchIcon /><input type="search" placeholder={t('nodes.search')} value={query} onChange={event => setQuery(event.target.value)} /></label>
        <div className="sort-control"><span>{t('nodes.sort')}</span><select value={sortKey} onChange={event => { sortTouched.current = true; setSortKey(event.target.value as SortKey) }}>{Object.entries(sortLabels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select><button title={t('nodes.toggleSort')} onClick={() => { sortTouched.current = true; setSortDirection(direction => direction === 'asc' ? 'desc' : 'asc') }}>{sortDirection === 'asc' ? '↑' : '↓'}</button></div>
      </div>
      <div className="node-panel-meta"><span>{t('nodes.count', { count: filteredNodes.length })}</span><span>{t('nodes.perPage', { count: pageSize })}</span></div>
      {loadingNodes ? <NodeSkeleton /> : visibleNodes.length > 0 ? <div className="node-grid">{visibleNodes.map(node => <NodeCard key={node.ip} node={node} protocolPriority={protocolPriority} active={status?.activeIp === node.ip} activeProtocol={status?.activeIp === node.ip ? status?.activeProtocol : undefined} connecting={status?.connectingIp === node.ip} reloadStatus={loadStatus} reloadNodes={loadNodes} />)}</div> : <EmptyState query={deferredQuery} />}
      {pageCount > 1 && <Pagination page={currentPage} pageCount={pageCount} onChange={setPage} />}
    </section>
  </div>
}

const NodeCard = memo(function NodeCard({ node, protocolPriority, active, activeProtocol, connecting, reloadStatus, reloadNodes }: {
  node: Node
  protocolPriority: Protocol[]
  active: boolean
  activeProtocol?: Protocol
  connecting: boolean
  reloadStatus: () => Promise<void>
  reloadNodes: () => Promise<void>
}) {
  const { t, i18n } = useTranslation()
  const locale = resolvedLocale(i18n.resolvedLanguage)
  const [testing, setTesting] = useState(false)
  const [connectionError, setConnectionError] = useState('')
  const [speedTestError, setSpeedTestError] = useState('')
  const [selecting, setSelecting] = useState(false)
  const [selectedProtocol, setSelectedProtocol] = useState<Protocol | ''>(activeProtocol || '')
  const availableProtocols = useMemo(() => orderedProtocols(node.protocols, protocolPriority), [node.protocols, protocolPriority])

  useEffect(() => {
    if (active && activeProtocol) setSelectedProtocol(activeProtocol)
  }, [active, activeProtocol])

  const select = async () => {
    setConnectionError(''); setSelecting(true)
    try {
      await api.post('/api/nodes/select', { ip: node.ip, protocol: selectedProtocol })
      await reloadStatus()
    } catch (reason) {
      setConnectionError(errorMessage(reason))
    } finally {
      setSelecting(false)
    }
  }

  const test = async () => {
    setSpeedTestError(''); setTesting(true)
    try {
      await api.post(`/api/nodes/${encodeURIComponent(node.ip)}/speed-test`)
      while (true) {
        await delay(1_500)
        const result = await api.get<{ state: string; error?: string }>(`/api/nodes/${encodeURIComponent(node.ip)}/speed-test`)
        if (result.state === 'failed') throw new Error(result.error || t('node.speedTestFailed'))
        if (result.state === 'complete') { await reloadNodes(); return }
      }
    } catch (reason) {
      setSpeedTestError(errorMessage(reason))
    } finally {
      setTesting(false)
    }
  }

  return <article className={`node-card ${active ? 'active' : ''}`}>
    <div className="node-identity">
      <div className="country-mark" aria-label={node.countryLong}>{countryFlag(node.countryShort)}</div>
      <div><div className="node-title"><strong>{node.countryShort || '—'}</strong>{active && <span className="live-badge">{t('node.active')}</span>}{connecting && <span className="pending-badge">{t('node.connecting')}</span>}</div><span className="mono">{node.ip}</span></div>
    </div>
    <p className="node-operator" title={node.message}>{node.operator || node.hostName || t('node.unknownOperator')}</p>
    <div className="protocol-list">{availableProtocols.map(protocol => <span key={protocol}>{protocolLabels[protocol]}</span>)}</div>
    <dl className="node-metrics">
      <div><dt>{t('node.vpnGate')}</dt><dd>{formatRate(node.speedBps)}</dd></div>
      <div><dt>{t('node.measured')}</dt><dd className={speedTestError || node.speedTestFailed ? 'failed' : node.measuredBps ? 'accent' : ''}>{speedTestError ? <ErrorTooltip label={t('node.failed')} message={speedTestError} /> : node.speedTestFailed ? t('node.failed') : formatRate(node.measuredBps)}</dd></div>
      <div><dt>{t('node.latency')}</dt><dd>{node.pingMs > 0 ? `${node.pingMs} ms` : '—'}</dd></div>
      <div><dt>{t('node.score')}</dt><dd>{formatNumber(node.score, locale)}</dd></div>
    </dl>
    <label className="connection-protocol"><span>{t('node.protocol')}</span><select value={selectedProtocol} disabled={connecting || selecting} onChange={event => { setSelectedProtocol(event.target.value as Protocol | ''); setConnectionError('') }}><option value="">{t('node.priority')}</option>{availableProtocols.map(protocol => <option value={protocol} key={protocol}>{protocolLabels[protocol]}</option>)}</select></label>
    <div className="node-actions"><button className={`secondary-button ${connectionError ? 'error-tooltip' : ''}`} data-tooltip={connectionError || undefined} aria-label={connectionError ? t('node.connectFailedLabel', { error: connectionError }) : undefined} disabled={connecting || selecting} onClick={select}>{connecting || selecting ? <><Spinner />{t('node.connectingAction')}</> : connectionError ? t('node.connectFailed') : active ? selectedProtocol === '' ? t('node.reconnectPriority') : selectedProtocol === activeProtocol ? t('node.reconnect') : t('node.reconnectProtocol') : t('node.switch')}</button><button className="icon-button" title={t('node.speedTestTitle')} disabled={testing} onClick={test}>{testing ? <Spinner /> : <SpeedIcon />}<span>{testing ? t('node.testing') : t('node.speedTest')}</span></button></div>
  </article>
})

function ErrorTooltip({ label, message }: { label: string; message: string }) {
  return <span className="error-tooltip" tabIndex={0} data-tooltip={message} aria-label={`${label}: ${message}`}>{label}</span>
}

function Pagination({ page, pageCount, onChange }: { page: number; pageCount: number; onChange: (page: number) => void }) {
  const { t } = useTranslation()
  const pages = Array.from({ length: Math.min(5, pageCount) }, (_, index) => {
    const start = Math.max(1, Math.min(page - 2, pageCount - 4))
    return start + index
  })
  return <nav className="pagination" aria-label={t('pagination.label')}><button disabled={page === 1} onClick={() => onChange(page - 1)}>{t('pagination.previous')}</button>{pages.map(value => <button className={value === page ? 'active' : ''} key={value} onClick={() => onChange(value)}>{value}</button>)}<button disabled={page === pageCount} onClick={() => onChange(page + 1)}>{t('pagination.next')}</button></nav>
}

function NodeSkeleton() {
  return <div className="node-grid">{Array.from({ length: 6 }, (_, index) => <div className="node-card skeleton-card" key={index}><i /><i /><i /><i /></div>)}</div>
}

function EmptyState({ query }: { query: string }) {
  const { t } = useTranslation()
  return <div className="empty-state"><SearchIcon /><h3>{query ? t('nodes.noMatches') : t('nodes.noNodes')}</h3><p>{query ? t('nodes.shortenSearch') : t('nodes.refreshFirst')}</p></div>
}

function Settings() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<Config>()
  const [newPassword, setNewPassword] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [restartRequired, setRestartRequired] = useState(false)
  const [saving, setSaving] = useState(false)
  const [restarting, setRestarting] = useState(false)

  const load = useCallback(async () => {
    try { setSettings(await api.get<Config>('/api/config')); setError('') }
    catch (reason) { setError(errorMessage(reason)) }
  }, [])
  useEffect(() => { void load() }, [load])

  const update = <Key extends keyof Config>(key: Key, value: Config[Key]) => setSettings(current => current ? { ...current, [key]: value } : current)
  const moveProtocol = (index: number, offset: number) => {
    if (!settings) return
    const target = index + offset
    if (target < 0 || target >= settings.protocolPriority.length) return
    const protocols = [...settings.protocolPriority]
    ;[protocols[index], protocols[target]] = [protocols[target], protocols[index]]
    update('protocolPriority', protocols)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError(''); setMessage('')
    if (!settings) return
    setSaving(true)
    try {
      const result = await api.put<{ restartRequired: boolean }>('/api/config', { config: settings, newPassword })
      setRestartRequired(result.restartRequired)
      setMessage(result.restartRequired ? t('settings.savedRestart') : t('settings.saved'))
      setNewPassword('')
      await load()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSaving(false)
    }
  }

  const restart = async () => {
    if (!window.confirm(t('settings.restartConfirm'))) return
    setError(''); setMessage(t('settings.restarting')); setRestarting(true)
    try {
      await api.post('/api/restart')
      window.setTimeout(() => window.location.reload(), 2_500)
    } catch (reason) {
      setError(errorMessage(reason)); setMessage(''); setRestarting(false)
    }
  }

  if (!settings) return <div className="panel-loading"><Spinner />{t('settings.loading')}</div>
  return <form className="page-stack settings-page" onSubmit={submit}>
    <header className="page-heading"><div><p className="eyebrow">{t('settings.eyebrow')}</p><h1>{t('settings.heading')}</h1><p>{t('settings.description')}</p></div></header>
    {error && <Notice tone="danger">{error}</Notice>}
    {message && <Notice tone={restartRequired ? 'warning' : 'success'}>{message}</Notice>}

    <SettingsSection index="01" title={t('settings.sourceTitle')} description={t('settings.sourceDescription')}>
      <Field label={t('settings.sourceUrl')} wide><input value={settings.sourceUrl} onChange={event => update('sourceUrl', event.target.value)} /></Field>
      <Field label={t('settings.refreshInterval')} hint={t('settings.durationExample')}><input value={settings.refreshInterval} onChange={event => update('refreshInterval', event.target.value)} /></Field>
      <Field label={t('settings.connectTimeout')} hint={t('settings.connectTimeoutHint')}><input value={settings.connectTimeout} onChange={event => update('connectTimeout', event.target.value)} /></Field>
      <Field label={t('settings.automaticSort')}><select value={settings.selectionMode} onChange={event => update('selectionMode', event.target.value as Config['selectionMode'])}><option value="speed">{t('settings.sortSpeed')}</option><option value="ping">{t('settings.sortPing')}</option><option value="score">{t('settings.sortScore')}</option></select></Field>
      <label className="toggle-field"><input type="checkbox" checked={settings.followRankingOnRefresh} onChange={event => update('followRankingOnRefresh', event.target.checked)} /><span><strong>{t('settings.followRanking')}</strong><small>{t('settings.followRankingHint')}</small></span></label>
      <Field label={t('settings.filter')} hint={t('settings.filterHint')} wide><textarea rows={6} spellCheck={false} value={settings.filterExpression} onChange={event => update('filterExpression', event.target.value)} /></Field>
      <FilterHelp />
    </SettingsSection>

    <SettingsSection index="02" title={t('settings.protocolTitle')} description={t('settings.protocolDescription')}>
      <div className="protocol-order">{settings.protocolPriority.map((protocol, index) => <div key={protocol}><b>{String(index + 1).padStart(2, '0')}</b><span>{protocolLabels[protocol]}</span><button type="button" disabled={index === 0} onClick={() => moveProtocol(index, -1)}>↑</button><button type="button" disabled={index === settings.protocolPriority.length - 1} onClick={() => moveProtocol(index, 1)}>↓</button></div>)}</div>
      <div className="settings-grid nested"><Field label={t('settings.vpnUsername')}><input value={settings.vpnGateUsername} onChange={event => update('vpnGateUsername', event.target.value)} /></Field><Field label={t('settings.vpnPassword')}><input type="password" value={settings.vpnGatePassword} onChange={event => update('vpnGatePassword', event.target.value)} /></Field><Field label={t('settings.psk')}><input type="password" value={settings.vpnGatePreSharedKey} onChange={event => update('vpnGatePreSharedKey', event.target.value)} /></Field></div>
    </SettingsSection>

    <SettingsSection index="03" title={t('settings.socksTitle')} description={t('settings.socksDescription')}>
      <Field label={t('settings.listenAddress')}><input value={settings.socks5.listenAddress} onChange={event => update('socks5', { ...settings.socks5, listenAddress: event.target.value })} /></Field>
      <Field label={t('settings.username')} hint={t('settings.noAuthHint')}><input value={settings.socks5.username} onChange={event => update('socks5', { ...settings.socks5, username: event.target.value })} /></Field>
      <Field label={t('settings.password')}><input type="password" value={settings.socks5.password} onChange={event => update('socks5', { ...settings.socks5, password: event.target.value })} /></Field>
    </SettingsSection>

    <SettingsSection index="04" title={t('settings.probeTitle')} description={t('settings.probeDescription')}>
      <Field label={t('settings.dnsServers')} hint={t('settings.dnsHint')} wide><input value={settings.dnsServers.join(', ')} onChange={event => update('dnsServers', event.target.value.split(',').map(value => value.trim()))} /></Field>
      <Field label={t('settings.speedUrl')} hint={t('settings.speedUrlHint')} wide><input type="url" value={settings.speedTestUrl} onChange={event => update('speedTestUrl', event.target.value)} /></Field>
      <Field label={t('settings.speedTimeout')} hint={t('settings.speedTimeoutHint')}><input value={settings.speedTestTimeout} onChange={event => update('speedTestTimeout', event.target.value)} /></Field>
      <Field label={t('settings.healthUrl')} wide><input value={settings.monitor.url} onChange={event => update('monitor', { ...settings.monitor, url: event.target.value })} /></Field>
      <Field label={t('settings.checkInterval')}><input value={settings.monitor.interval} onChange={event => update('monitor', { ...settings.monitor, interval: event.target.value })} /></Field>
      <Field label={t('settings.requestTimeout')}><input value={settings.monitor.timeout} onChange={event => update('monitor', { ...settings.monitor, timeout: event.target.value })} /></Field>
    </SettingsSection>

    <SettingsSection index="05" title={t('settings.adminTitle')} description={t('settings.adminDescription')}>
      <Field label={t('settings.databasePath')}><input value={settings.databasePath} onChange={event => update('databasePath', event.target.value)} /></Field>
      <Field label={t('settings.webListen')}><input value={settings.web.listenAddress} onChange={event => update('web', { ...settings.web, listenAddress: event.target.value })} /></Field>
      <Field label={t('settings.webUsername')}><input value={settings.web.username} onChange={event => update('web', { ...settings.web, username: event.target.value })} /></Field>
      <Field label={t('settings.newPassword')} hint={t('settings.newPasswordHint')}><input type="password" minLength={8} value={newPassword} onChange={event => setNewPassword(event.target.value)} /></Field>
    </SettingsSection>

    <div className="settings-actions"><div><strong>{restartRequired ? t('settings.restartPending') : t('settings.writesLocal')}</strong><small>{t('settings.sensitiveHint')}</small></div><button type="button" className="danger-button" disabled={restarting} onClick={restart}>{restarting ? <Spinner /> : <RestartIcon />}{t('settings.restart')}</button><button className="primary-button" disabled={saving}>{saving ? <Spinner /> : null}{t('settings.save')}</button></div>
  </form>
}

function SettingsSection({ index, title, description, children }: { index: string; title: string; description: string; children: ReactNode }) {
  return <section className="settings-section"><header><span>{index}</span><div><h2>{title}</h2><p>{description}</p></div></header><div className="settings-grid">{children}</div></section>
}

function FilterHelp() {
  const { t } = useTranslation()
  return <aside className="filter-help">
    <div className="filter-help-intro"><strong>{t('filter.rules')}</strong><p>{t('filter.intro')}</p></div>
    <div className="filter-help-grid">
      <section><strong>{t('filter.textFields')}</strong><code>hostName</code><code>ip</code><code>countryLong</code><code>countryShort</code><code>logType</code><code>operator</code><code>operatorMessage</code></section>
      <section><strong>{t('filter.numberFields')}</strong><code>score</code><code>pingMs</code><code>speedBps</code><code>sessions</code><code>uptimeMs</code><code>totalUsers</code><code>totalTraffic</code></section>
      <section><strong>{t('filter.protocols')}</strong><code>protocols</code><p>{t('filter.protocolsHint')}</p></section>
      <section><strong>{t('filter.helpers')}</strong><code>cidrContains(cidr, ip)</code><code>includesIgnoreCase(value, search)</code><p>{t('filter.helpersHint')}</p></section>
    </div>
    <div className="filter-units"><span><code>pingMs</code>: {t('filter.milliseconds')}</span><span><code>speedBps</code>: bit/s</span><span><code>uptimeMs</code>: {t('filter.milliseconds')}</span><span><code>totalTraffic</code>: bytes</span></div>
    <strong className="filter-example-title">{t('filter.example')}</strong>
    <pre><code>{`node.countryShort === "JP" &&
node.pingMs > 0 && node.pingMs < 100 &&
node.protocols.includes("openvpn_udp") &&
!includesIgnoreCase(node.operatorMessage, "academic use only")`}</code></pre>
    <p className="filter-help-note">{t('filter.operators')} <code>cidrContains("219.100.0.0/16", node.ip)</code></p>
  </aside>
}

function LanguageSelect() {
  const { t, i18n } = useTranslation()
  const locale = resolvedLocale(i18n.resolvedLanguage)

  return <select
    className="locale-select"
    aria-label={t('language.label')}
    value={locale}
    onChange={event => void i18n.changeLanguage(event.target.value)}
  >
    {Object.entries(localeConfig.resources).map(([value, resource]) => <option value={value} key={value}>{resource.label}</option>)}
  </select>
}

function Field({ label, hint, wide, children }: { label: string; hint?: string; wide?: boolean; children: ReactNode }) {
  return <label className={`field ${wide ? 'wide' : ''}`}><span>{label}</span>{children}{hint && <small>{hint}</small>}</label>
}

function Notice({ tone, children }: { tone: 'danger' | 'warning' | 'success'; children: ReactNode }) {
  return <div className={`notice ${tone}`}><i />{children}</div>
}

function Spinner() { return <span className="spinner" aria-hidden="true" /> }
function RefreshIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.34 5.66M20 4v7h-7" /></svg> }
function RestartIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 4v6h6M20 20v-6h-6M5.5 15a8 8 0 0 0 13-3M18.5 9A8 8 0 0 0 5.5 6" /></svg> }
function SearchIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></svg> }
function SpeedIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 17a8 8 0 1 1 16 0M12 17l4-6" /><path d="M7 17h10" /></svg> }
function ThemeIcon({ theme }: { theme: Theme }) { return theme === 'dark' ? <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg> : <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 15.5A8.5 8.5 0 0 1 8.5 4 8.5 8.5 0 1 0 20 15.5Z" /></svg> }

function delay(milliseconds: number) { return new Promise(resolve => window.setTimeout(resolve, milliseconds)) }
function errorMessage(reason: unknown) { return reason instanceof Error ? reason.message : String(reason) }
function orderedProtocols(protocols: Protocol[], priority: Protocol[]) {
  const order = [...priority, ...protocolDisplayOrder.filter(protocol => !priority.includes(protocol))]
  return order.filter(protocol => protocols.includes(protocol))
}
function preferredTheme(): Theme {
  const saved = window.localStorage.getItem('proxygate:theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}
function normalizedPing(value: number) { return value > 0 ? value : Number.MAX_SAFE_INTEGER }
function stateLabel(state: string | undefined, t: TFunction) {
  const labels: Record<string, string> = {
    connected: t('status.connected'),
    connecting: t('status.connecting'),
    error: t('status.error'),
    idle: t('status.idle'),
  }
  return labels[state || ''] || state || t('status.loading')
}
function formatDateTime(value: string, locale: string) { return new Date(value).toLocaleString(locale, { hour12: false }) }
function formatRelativeTime(value: string | Date | undefined, t: TFunction) {
  if (!value) return t('time.never')
  const milliseconds = Date.now() - new Date(value).getTime()
  if (milliseconds < 10_000) return t('time.now')
  if (milliseconds < 60_000) return t('time.secondsAgo', { count: Math.floor(milliseconds / 1_000) })
  if (milliseconds < 3_600_000) return t('time.minutesAgo', { count: Math.floor(milliseconds / 60_000) })
  return t('time.hoursAgo', { count: Math.floor(milliseconds / 3_600_000) })
}
function formatNumber(value: number, locale: string) { return new Intl.NumberFormat(locale, { notation: value >= 10_000 ? 'compact' : 'standard' }).format(value) }
function formatRate(bitsPerSecond: number) {
  if (!bitsPerSecond) return '—'
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps']
  let value = bitsPerSecond; let unit = 0
  while (value >= 1_000 && unit < units.length - 1) { value /= 1_000; unit++ }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[unit]}`
}
function countryFlag(code: string) {
  const normalized = code.toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return '◎'
  return String.fromCodePoint(...[...normalized].map(character => 127397 + character.charCodeAt(0)))
}

export default App
