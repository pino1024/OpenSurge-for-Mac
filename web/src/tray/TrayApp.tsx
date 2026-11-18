import { releaseDisplayVersion } from '../release'
import { useCallback, useEffect, useRef, useState } from 'react'
import { copyText, desktopAction, isDesktop } from '../desktop'
import { activateLanguage, cacheRequestedLanguage, initialRequestedLanguage, isRequestedLanguage, prepareLanguage, t } from '../i18n'
import { useTheme } from '../hooks/useTheme'
import { api, waitForOperation } from '../api'
import { gatewayIsTransitioning, gatewayStatusDetail, gatewayStatusLabel, recoveryLabel, statusLabel, takeoverLabel } from '../status'
import type { MenuBarIndicators, SleepPreventionStatus } from '../types'
import { isWindowVisible, watchVisibleRefresh } from '../visibility'
import type { MenuBarStatus, TraySnapshot } from './types'
import { TrayUtilities } from './TrayUtilities'
import { FanControl } from './FanControl'
import { TrayActivity } from './TrayActivity'
import { saveTrayLayout, trayLayout } from './trayLayout'
import './tray.css'

const initial: TraySnapshot = { status: null, indicator: 'connecting', sequence: 0, can_quit: false }
const defaultMenuBarMetrics: MenuBarIndicators = { download: true, cpu: true, memory: true, disk: true, temperature: true }

function menuBarMetrics(status: MenuBarStatus | null): MenuBarIndicators {
 return {
  download: status?.ui_preferences.menu_bar?.download ?? defaultMenuBarMetrics.download,
  cpu: status?.ui_preferences.menu_bar?.cpu ?? defaultMenuBarMetrics.cpu,
  memory: status?.ui_preferences.menu_bar?.memory ?? defaultMenuBarMetrics.memory,
  disk: status?.ui_preferences.menu_bar?.disk ?? defaultMenuBarMetrics.disk,
  temperature: status?.ui_preferences.menu_bar?.temperature ?? defaultMenuBarMetrics.temperature,
 }
}

export function diagnosticSummary(status: MenuBarStatus | null): string {
 if (!status) return 'OpenSurge Control API: unreachable'
 return ['OpenSurge for Mac', `Gateway: ${status.gateway}`, `Topology: ${status.topology}`, `LAN IP: ${status.lan_ip}`, `DHCP/DNS: ${status.dhcp}`, `mihomo: ${status.mihomo}`, `TUN: ${status.tun}${status.tun_interface ? ` [${status.tun_interface}]` : ''}`, `PF: ${status.pf_anchor}`, `Forwarding: ${status.forwarding}`, `IPv4 takeover: ${status.ipv4_takeover}`, `IPv6 takeover: ${status.ipv6_takeover}`, `Clients: ${status.devices_error ? `${status.client_count} (incomplete: ${status.devices_error})` : status.client_count}`, `Drift: ${status.drift}`, `Recovery: ${status.recovery_required ? status.recovery_stage ?? 'required' : 'none'}`, `Error code: ${status.error_code ?? 'none'}`, `Lid-closed sleep prevention: ${status.sleep_prevention.active ? 'active' : 'off'}`].join('\n')
}

async function restoreBypassRouting() {
 await api.setLocalRouting('rule')
 await api.refreshLocalConnections().catch(() => {})
}

export function TrayMenuApp() { return <TrayApp menuOnly /> }

export function TrayApp({ menuOnly = false }: { menuOnly?: boolean } = {}) {
 const panel = useRef<HTMLElement>(null)
 useEffect(() => {
  // WKWebView otherwise focuses the first action when the native popup opens.
  // Start on the panel; Tab still reaches More actions and retains its focus ring.
  let visible = false
  const focusPanel = () => {
   const next = isWindowVisible()
   if (next && !visible) panel.current?.focus({ preventScroll: true })
   visible = next
  }
  focusPanel()
  document.addEventListener('visibilitychange', focusPanel)
  return () => document.removeEventListener('visibilitychange', focusPanel)
 }, [])
 const [, , theme] = useTheme()
 useEffect(() => { void desktopAction('tray-appearance', { theme }).catch(() => {}) }, [theme])
 const more = useRef<HTMLDetailsElement>(null)
 const [nativeMenuOpen, setNativeMenuOpen] = useState(false)
 const nativeMenu = isDesktop()
 useEffect(() => {
  const close = (event: PointerEvent) => { if (more.current && !more.current.contains(event.target as Node)) more.current.open = false }
  const checkUpdate = () => { if (more.current) more.current.open = true }
  const menuState = (event: Event) => setNativeMenuOpen((event as CustomEvent<boolean>).detail)
  document.addEventListener('pointerdown', close)
  window.addEventListener('opensurge:check-update', checkUpdate)
  window.addEventListener('opensurge:tray-menu', menuState)
  return () => { document.removeEventListener('pointerdown', close); window.removeEventListener('opensurge:check-update', checkUpdate); window.removeEventListener('opensurge:tray-menu', menuState) }
 }, [])
 const [snapshot, setSnapshot] = useState(initial)
 const [language, setLanguage] = useState(initialRequestedLanguage)
 const [error, setError] = useState('')
 const [copied, setCopied] = useState(false)
 const [sleepBusy, setSleepBusy] = useState(false)
 const [metricsOverride, setMetricsOverride] = useState<MenuBarIndicators | null>(null)
 const metricsBusy = useRef(false)
 const [refreshing, setRefreshing] = useState(false)
 const [serviceBusy, setServiceBusy] = useState(false)
 const [gatewayBusy, setGatewayBusy] = useState(false)
 const [browserBusy, setBrowserBusy] = useState(false)
 const [browserError, setBrowserError] = useState('')
 const browserPending = useRef(false)
 const [networkExpanded, setNetworkExpanded] = useState(() => trayLayout().network)
 const [preferencesOpen, setPreferencesOpen] = useState(() => trayLayout().preferences)
 const toggleNetwork = () => setNetworkExpanded(value => { saveTrayLayout({ network: !value }); return !value })
 const togglePreferences = () => setPreferencesOpen(value => { saveTrayLayout({ preferences: !value }); return !value })
 const sequence = useRef(0)
 const active = useRef(true)
 const sleepGeneration = useRef(0)
 const sleepPending = useRef(false)
 const languageRef = useRef(language)
 languageRef.current = language
 const refresh = useCallback(async (force = false) => {
  const generation = sleepGeneration.current
  try {
   const next = await desktopAction<TraySnapshot>('menubar-status', { refresh: force })
   if (!active.current || next.sequence < sequence.current) return
   sequence.current = next.sequence
   const requested = next.status?.ui_preferences.language
   if (isRequestedLanguage(requested) && languageRef.current !== requested) {
    await prepareLanguage(requested)
    if (!active.current || next.sequence < sequence.current) return
    activateLanguage(requested)
    cacheRequestedLanguage(requested)
    languageRef.current = requested
    setLanguage(requested)
   }
   setSnapshot(current => {
    if (next.status && current.status && (sleepPending.current || generation !== sleepGeneration.current)) {
     return { ...next, status: { ...next.status, sleep_prevention: current.status.sleep_prevention } }
    }
    return next
   })
  } catch { if (active.current) setSnapshot(current => ({ ...current, status: null, indicator: 'unreachable', can_quit: false })) }
 }, [])
 useEffect(() => {
  active.current = true
  const stop = watchVisibleRefresh(refresh, 1000)
  return () => { active.current = false; stop() }
 }, [refresh])

 const run = async (action: () => Promise<unknown>) => {
  setError('')
  try { await action() } catch (cause) { if (active.current) setError(cause instanceof Error ? cause.message : String(cause)) }
 }
 const show = (page: string, options?: { owner?: string; section?: 'active-devices' }) => void run(() => desktopAction('show-main', { page, ...options }))
 const openBrowser = async () => {
  if (browserPending.current) return
  browserPending.current = true; setBrowserBusy(true); setBrowserError('')
  try { await desktopAction('open-browser') }
  catch (cause) { if (active.current) setBrowserError(cause instanceof Error ? cause.message : String(cause)) }
  finally { browserPending.current = false; if (active.current) setBrowserBusy(false) }
 }
 const serviceAction = async (action: 'reconnect' | 'quit', full = false) => {
  if (serviceBusy) return
  setServiceBusy(true)
  await run(() => desktopAction(action, action === 'quit' ? { full } : {}))
  setServiceBusy(false)
  await refresh(true)
 }
 const changeMetric = async (key: keyof MenuBarIndicators, value: boolean) => {
  if (metricsBusy.current || !snapshot.status) return
  metricsBusy.current = true
  const next = { ...menuBarMetrics(snapshot.status), ...metricsOverride, [key]: value }
  setMetricsOverride(next)
  await run(async () => {
   const saved = await api.setUIPreferences({ menu_bar: next })
   setMetricsOverride({
    download: saved.menu_bar?.download ?? next.download,
    cpu: saved.menu_bar?.cpu ?? next.cpu,
    memory: saved.menu_bar?.memory ?? next.memory,
    disk: saved.menu_bar?.disk ?? next.disk,
    temperature: saved.menu_bar?.temperature ?? next.temperature,
   })
   await refresh(true)
  })
  setMetricsOverride(null)
  metricsBusy.current = false
 }
 const toggleBypass = async () => {
  if (!status || gatewayBusy) return
  const control = bypassControl(snapshot.indicator, status.topology)
  if (!control.enabled) return
  setGatewayBusy(true)
  setError('')
  try {
   if (control.action === 'cleanup-start') {
    await waitForOperation((await api.gateway('stop')).id)
    await waitForOperation((await api.gateway('start')).id)
    await restoreBypassRouting()
   } else if (control.action === 'start' || control.action === 'stop') {
    await waitForOperation((await api.gateway(control.action)).id)
    if (control.action === 'start') await restoreBypassRouting()
   }
   await refresh(true)
  } catch (cause) {
   if (active.current) setError(cause instanceof Error ? cause.message : String(cause))
  } finally {
   if (active.current) setGatewayBusy(false)
  }
 }
 const changeSleep = async (enabled: boolean) => {
  if (sleepPending.current) return
  sleepPending.current = true
  sleepGeneration.current++
  setSleepBusy(true)
  await run(async () => {
   const sleep = await desktopAction<SleepPreventionStatus>('sleep-prevention', { enabled })
   sleepGeneration.current++
   setSnapshot(current => current.status ? { ...current, status: { ...current.status, sleep_prevention: sleep } } : current)
  })
  sleepGeneration.current++
  sleepPending.current = false
  setSleepBusy(false)
  await refresh(true)
 }
 const status = snapshot.status
 const displayLabel = gatewayStatusLabel(snapshot.indicator)
 const detail = gatewayStatusDetail(status?.presentation)
 const transitioning = gatewayIsTransitioning(snapshot.indicator)
 const topology = status ? ({ same_lan: '旁路由模式', same_wifi_dhcp: '局域网 DHCP 接管', isolated_lan: '独立下游 LAN' } as Record<string, string>)[status.topology] ?? status.topology : ''
 const rows = status ? [
  [t('网关'), displayLabel], [t('拓扑'), t(topology)], ['LAN IP', status.lan_ip], [t('客户端'), status.devices_error ? t('数据不完整') : String(status.client_count)],
  ['DHCP / DNS', statusLabel(status.dhcp)], ['mihomo', status.mihomo.startsWith('running') ? `${statusLabel('running')}${status.mihomo.slice(7)}` : statusLabel(status.mihomo)], ['TUN', `${takeoverLabel(status.tun)}${status.tun_interface ? ` · ${status.tun_interface}` : ''}`],
  ['PF', t(status.pf_anchor === 'loaded' ? '已加载' : '未加载')], [t('IPv4 接管'), takeoverLabel(status.ipv4_takeover)], [t('IPv6 接管'), takeoverLabel(status.ipv6_takeover)],
 ] : []
 const bypass = bypassControl(snapshot.indicator, status?.topology)
 const menu = <div className="tray-more-panel">
  <div className="tray-more-content">
   <button className="tray-browser" disabled={browserBusy} onClick={() => void openBrowser()}>{t(browserBusy ? '正在打开浏览器…' : '在浏览器中打开')}</button>
   {browserError && <p role="alert" className="tray-error">{browserError}</p>}
   <div className="tray-row"><button onClick={() => void run(async () => { await copyText(diagnosticSummary(status)); setCopied(true) })}>{t(copied ? '已复制' : '复制诊断摘要')}</button><button disabled={refreshing} aria-label={t('刷新状态')} onClick={() => { setRefreshing(true); void refresh(true).finally(() => setRefreshing(false)) }}>{t(refreshing ? '正在刷新…' : '刷新')}</button></div>
   <TrayUtilities canUninstall={snapshot.can_uninstall === true} />
   {menuOnly && error && <p role="alert" className="tray-error">{error}</p>}
   <p className="tray-caption">{releaseDisplayVersion(import.meta.env.VITE_OPENSURGE_RELEASE_TAG)}</p>
  </div>
  <section className="tray-exit">
   <button disabled={!snapshot.can_quit || !snapshot.service_actions || serviceBusy} title={!snapshot.can_quit ? t('请先在网络设置中停止网关并完成恢复。') : undefined} onClick={() => void serviceAction('quit', true)}>{t('退出 OpenSurge…')}</button>
   <button disabled={serviceBusy} onClick={() => void serviceAction('quit')}>{t('只退出桌面 App…')}</button>
  </section>
 </div>
 if (menuOnly) return <main className="tray-app tray-menu-app" ref={panel} tabIndex={-1} role="dialog" aria-label={t('更多操作')}>{menu}</main>
 return <main className="tray-app" ref={panel} tabIndex={-1}>
  <header className="tray-header">
   <img src="/opensurge-icon.png" alt="" /><h1>OpenSurge</h1>
   <div className="tray-header-actions">
   {status && <button type="button" className="tray-bypass" disabled={!bypass.enabled || gatewayBusy} aria-label={t(bypass.label)} title={t(bypass.title)} onClick={() => void toggleBypass()}>{t(gatewayBusy ? bypass.busy : bypass.text)}</button>}
   <span className={`tray-indicator ${snapshot.indicator}`} title={detail || displayLabel}><span className={`tray-dot ${transitioning ? 'transition' : snapshot.indicator}`} />{displayLabel}</span>
   {nativeMenu ? <button className="tray-more-trigger" aria-label={t('更多操作')} title={t('更多操作')} aria-haspopup="dialog" aria-expanded={nativeMenuOpen} onClick={() => void run(() => desktopAction('toggle-tray-menu'))}>···</button> : <details className="tray-more" ref={more} onKeyDown={event => { if (event.key === 'Escape' && more.current?.open) { more.current.open = false; event.stopPropagation(); more.current.querySelector('summary')?.focus() } }}>
    <summary aria-label={t('更多操作')} title={t('更多操作')}>···</summary>
    {menu}
   </details>}
   </div>
  </header>
  {status && <p className="tray-subtitle"><span>{t(topology)}</span><span aria-hidden="true"> · </span><span>{status.lan_ip}</span></p>}
  <div className="tray-content">
   {snapshot.indicator === 'recovery' && <section className="tray-notice warning" role="alert"><strong>{t('网络恢复尚未完成')}</strong><p>{recoveryLabel(status?.recovery_stage ?? '')}</p><button onClick={() => show('network')}>{t('继续恢复')}</button></section>}
   {snapshot.indicator === 'interrupted' && <section className="tray-notice warning" role="alert"><strong>{displayLabel}</strong><p>{t(status?.topology === 'same_wifi_dhcp' ? '请在网络设置中安全清理旧状态，再继续 DHCP 接管流程。' : '点标题栏“清理并开启”会先清理重启遗留状态，再启动旁路由。')}</p>{status?.topology === 'same_wifi_dhcp' ? <button onClick={() => show('network')}>{t('查看网络设置')}</button> : <button disabled={!bypass.enabled || gatewayBusy} onClick={() => void toggleBypass()}>{t(gatewayBusy ? '正在清理重启遗留状态…' : '清理并开启')}</button>}</section>}
   {snapshot.indicator === 'degraded' && <section className="tray-notice warning" role="alert"><strong>{displayLabel}</strong><p>{detail || t('请在网络设置中查看异常原因。')}</p><button onClick={() => show('network')}>{t('查看网络设置')}</button></section>}
   {(transitioning || snapshot.indicator === 'unknown') && <section className="tray-notice" role="status"><strong>{displayLabel}</strong>{detail && <p>{detail}</p>}</section>}
   {!status && <section className="tray-notice" role="status"><p>{t(snapshot.indicator === 'connecting' ? '正在连接后台服务…' : '状态暂不可用。后台连接恢复后会自动更新。')}</p>{snapshot.service_actions && <button disabled={serviceBusy} onClick={() => void serviceAction('reconnect')}>{t('重新连接后台服务')}</button>}</section>}
   {status?.drift && !transitioning && <p className="tray-inline-warning">{t(status.gateway === 'stopped' ? '配置将在下次启动时应用' : '有配置待应用，当前仍使用原配置')}</p>}
   {status?.presentation?.diagnosis_warning && <p className="tray-inline-warning">{t('上次诊断有待检查项')} <button className="tray-text-button" onClick={() => show('diagnostics')}>{t('查看诊断')}</button></p>}
   <TrayActivity gateway={status?.gateway} onOpen={show} />
   <section className={`tray-network ${networkExpanded ? 'expanded' : ''}`}>
    <button type="button" className="tray-network-toggle" aria-expanded={networkExpanded} aria-controls="tray-network-details" onClick={toggleNetwork}><span>{t('网络状态')}</span><span className="tray-caption">{t(networkExpanded ? '收起详情' : '查看详情')} <svg className="tray-disclosure" viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2 4 4-4 4" /></svg></span></button>
    <div className="tray-network-reveal" id="tray-network-details" aria-hidden={!networkExpanded} inert={!networkExpanded}>
     <div className="tray-network-inner">
      {status ? <dl className="tray-status">{rows.map(([name, value]) => <div key={name}><dt>{name}</dt><dd title={value}>{value}</dd></div>)}</dl> : <p className="tray-hint">{t('状态暂不可用。后台连接恢复后会自动更新。')}</p>}
      {status?.recovery_required && status.recovery_stage === 'prepared' && <p className="tray-hint">{t('恢复资料已准备；尚未改动网络')}</p>}
      <button className="tray-text-button" onClick={() => show('network')}>{t('查看网络设置')} ›</button>
     </div>
    </div>
   </section>
   <section className={`tray-preferences ${preferencesOpen ? 'expanded' : ''}`}>
    <button type="button" className="tray-network-toggle" aria-expanded={preferencesOpen} aria-controls="tray-menu-metrics" onClick={togglePreferences}><span>{t('菜单栏指标')}</span><span className="tray-caption">{t(preferencesOpen ? '收起详情' : '查看详情')} <svg className="tray-disclosure" viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2 4 4-4 4" /></svg></span></button>
    <div className="tray-network-reveal" id="tray-menu-metrics" aria-hidden={!preferencesOpen} inert={!preferencesOpen}><div className="tray-network-inner">
    {(['download', 'cpu', 'memory', 'disk', 'temperature'] as const).map(key => {
     const label = key === 'download' ? t('下行速率') : key === 'cpu' ? t('CPU 占用') : key === 'memory' ? t('内存占用') : key === 'disk' ? t('磁盘占用') : t('CPU 温度')
     const metrics = metricsOverride ?? menuBarMetrics(status)
     return <label key={key}><span>{label}</span><input type="checkbox" role="switch" aria-label={label} checked={metrics[key]} disabled={!status} onChange={event => void changeMetric(key, event.target.checked)} /></label>
    })}
    <FanControl />
    {snapshot.notebook === true && <>
     <label><span>{t('合盖保持运行')}<small>{t('默认关闭 · 本次运行有效')}</small></span><input type="checkbox" role="switch" aria-label={t('合盖保持运行')} checked={status?.sleep_prevention.active ?? false} disabled={!status || sleepBusy} onChange={event => void changeSleep(event.target.checked)} /></label>
     {status?.sleep_prevention.active && <p className="tray-notice warning">{t('合盖后仍会运行，请注意耗电与散热，不要放入不通风的包内。')}</p>}
     {status?.sleep_prevention.error && <p role="alert" className="tray-error">{status.sleep_prevention.error}</p>}
    </>}
    </div></div>
   </section>
   {error && <p role="alert" className="tray-error">{error}</p>}
  </div>
  <footer className="tray-footer"><button className="primary" onClick={() => show('dashboard')}><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M7 3H4a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2V9M9 2h5v5M7 9l7-7" /></svg>{t('打开 OpenSurge 面板')}</button></footer>
 </main>
}

type BypassAction = 'start' | 'stop' | 'cleanup-start'

export function bypassControl(indicator: string, topology?: string): { enabled: boolean; action: BypassAction; text: string; busy: string; label: string; title: string } {
 const bypass = topology === 'same_lan'
 const dhcp = topology === 'same_wifi_dhcp'
 const interrupted = indicator === 'interrupted'
 const on = indicator === 'running' || indicator === 'degraded'
 const action: BypassAction = interrupted ? 'cleanup-start' : on ? 'stop' : 'start'
 const enabled = Boolean(topology) && !dhcp && !gatewayIsTransitioning(indicator) && !['connecting', 'unreachable', 'unknown', 'recovery'].includes(indicator)
 const text = interrupted ? '清理并开启' : on ? '关闭' : '开启'
 const busy = interrupted ? '正在清理重启遗留状态…' : on ? '正在关闭…' : '正在开启…'
 const label = interrupted
  ? (bypass ? '清理重启遗留状态并开启旁路由' : '清理重启遗留状态并开启网关')
  : on
   ? (bypass ? '关闭旁路由' : '关闭网关')
   : (bypass ? '开启旁路由' : '开启网关')
 const title = dhcp ? '局域网 DHCP 接管请在网络设置中开关。' : interrupted ? '先清理重启遗留的旧运行状态，再启动旁路由。' : label
 return { enabled, action, text, busy, label, title }
}
