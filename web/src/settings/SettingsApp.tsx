import { LANAccessCard } from '../components/LANAccessCard'
import { FanControl } from '../tray/FanControl'
import { releaseDisplayVersion } from '../release'
import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { Select } from '../components/Select'
import { LoginItemFeedback } from '../components/LoginItemFeedback'
import { desktopAction } from '../desktop'
import { useDesktopUtilities } from '../hooks/useDesktopUtilities'
import { useDesktopRuntime } from '../hooks/useDesktopRuntime'
import { useInterfaceLanguage } from '../hooks/useInterfaceLanguage'
import { useTheme, type ThemePreference } from '../hooks/useTheme'
import { t, type RequestedLanguage } from '../i18n'
import type { MenuBarIndicators } from '../types'
import { watchVisibleRefresh } from '../visibility'
import '../styles.css'
import './settings.css'

export function SettingsApp() {
 const [error, setError] = useState('')
 const { language, languageChanging, changeLanguage, beginLanguageRefresh } = useInterfaceLanguage(setError, false)
 const [, , preference, setPreference] = useTheme()
 const { state, busy: utilityBusy, error: utilityError, setError: setUtilityError, check, changeLogin, openLink, uninstall } = useDesktopUtilities()
 const { snapshot, busy: runtimeBusy, error: runtimeError, changeSleep, quit } = useDesktopRuntime()
 const [metrics, setMetrics] = useState<MenuBarIndicators>({ download: true, cpu: true, memory: true, disk: true, temperature: true })
 const metricsBusy = useRef(false)
 const busy = utilityBusy || runtimeBusy
 const login = state?.login
 const update = state?.update
 const sleep = snapshot?.status?.sleep_prevention
 const canQuit = snapshot?.can_quit === true && snapshot.service_actions === true
 const canUninstall = snapshot?.can_uninstall === true && state?.uninstall === 'available'
 useEffect(() => { void desktopAction('settings-appearance', { theme: preference }).catch(() => {}) }, [preference])
 useEffect(() => {
  let active = true
  const stop = watchVisibleRefresh(async () => {
   const accept = beginLanguageRefresh()
   try { const next = await api.uiPreferences(); if (active) await accept(next.language) }
   catch { /* Keep the saved preference while the Control API reconnects. */ }
  }, 3000)
  return () => { active = false; stop() }
 }, [beginLanguageRefresh])
 useEffect(() => {
  let active = true
  const apply = (value: { menu_bar?: Partial<MenuBarIndicators> }) => {
   if (metricsBusy.current) return
   setMetrics({
    download: value.menu_bar?.download ?? true,
    cpu: value.menu_bar?.cpu ?? true,
    memory: value.menu_bar?.memory ?? true,
    disk: value.menu_bar?.disk ?? true,
    temperature: value.menu_bar?.temperature ?? true,
   })
  }
  const stop = watchVisibleRefresh(async () => {
   try { const next = await api.uiPreferences(); if (active) apply(next) }
   catch { /* Keep the last meters while the Control API reconnects. */ }
  }, 3000)
  return () => { active = false; stop() }
 }, [])
 const changeMetric = async (key: keyof MenuBarIndicators, value: boolean) => {
  if (metricsBusy.current) return
  const previous = metrics
  const next = { ...metrics, [key]: value }
  metricsBusy.current = true
  setMetrics(next)
  setError('')
  try {
   const saved = await api.setUIPreferences({ menu_bar: next })
   setMetrics({
    download: saved.menu_bar?.download ?? next.download,
    cpu: saved.menu_bar?.cpu ?? next.cpu,
    memory: saved.menu_bar?.memory ?? next.memory,
    disk: saved.menu_bar?.disk ?? next.disk,
    temperature: saved.menu_bar?.temperature ?? next.temperature,
   })
   void desktopAction('menubar-status', { refresh: true }).catch(() => {})
  } catch (cause) {
   setMetrics(previous)
   setError(cause instanceof Error ? cause.message : String(cause))
  } finally { metricsBusy.current = false }
 }
 return <main className="settings-app">
  <div className="desktop-titlebar" aria-hidden="true" />
  <header className="settings-header"><img src="/opensurge-icon.png" alt="" /><div>
   <h1>{t('OpenSurge 设置')}</h1><p>{t('个性化你的 OpenSurge 使用体验。')}</p>
   <nav className="settings-project-links" aria-label={t('项目链接')}>
    <a href="https://github.com/YTwsy/OpenSurge-for-Mac" target="_blank" rel="noreferrer" onClick={event => { event.preventDefault(); setUtilityError(''); openLink('open-external', { url: event.currentTarget.href }) }}>{t('GitHub 仓库')}<span aria-hidden="true">↗</span></a>
    <a href="https://opensurge.pages.dev/" target="_blank" rel="noreferrer" onClick={event => { event.preventDefault(); setUtilityError(''); openLink('open-external', { url: event.currentTarget.href }) }}>{t('文档')}<span aria-hidden="true">↗</span></a>
   </nav>
  </div></header>
  <section className="settings-section" aria-label={t('界面')}>
   <div className="settings-row"><label htmlFor="settings-language">{t('界面语言')}</label><Select id="settings-language" aria-label={t('界面语言')} value={language} disabled={languageChanging} onChange={value => { setError(''); void changeLanguage(value as RequestedLanguage) }}><option value="system">{t('跟随系统')}</option><option value="zh-Hans">简体中文</option><option value="en">English</option></Select></div>
   {languageChanging && <p className="settings-hint" role="status">{t('正在保存语言…')}</p>}
   <div className="settings-row"><label htmlFor="settings-appearance">{t('外观')}</label><Select id="settings-appearance" aria-label={t('外观')} value={preference} onChange={value => setPreference(value as ThemePreference)}><option value="system">{t('跟随系统')}</option><option value="light">{t('浅色模式')}</option><option value="dark">{t('深色模式')}</option></Select></div>
   <div className="settings-row"><span>{t('菜单栏指标')}<small>{t('显示在菜单栏图标旁边，可任意开关。')}</small></span></div>
   <label className="settings-row"><span>{t('下行速率')}</span><input type="checkbox" role="switch" aria-label={t('下行速率')} checked={metrics.download} disabled={busy} onChange={event => void changeMetric('download', event.target.checked)} /></label>
   <label className="settings-row"><span>{t('CPU 占用')}</span><input type="checkbox" role="switch" aria-label={t('CPU 占用')} checked={metrics.cpu} disabled={busy} onChange={event => void changeMetric('cpu', event.target.checked)} /></label>
   <label className="settings-row"><span>{t('内存占用')}</span><input type="checkbox" role="switch" aria-label={t('内存占用')} checked={metrics.memory} disabled={busy} onChange={event => void changeMetric('memory', event.target.checked)} /></label>
   <label className="settings-row"><span>{t('磁盘占用')}</span><input type="checkbox" role="switch" aria-label={t('磁盘占用')} checked={metrics.disk} disabled={busy} onChange={event => void changeMetric('disk', event.target.checked)} /></label>
   <label className="settings-row"><span>{t('CPU 温度')}</span><input type="checkbox" role="switch" aria-label={t('CPU 温度')} checked={metrics.temperature} disabled={busy} onChange={event => void changeMetric('temperature', event.target.checked)} /></label>
  </section>
  <section className="settings-section" aria-label={t('运行偏好')}>
   <label className="settings-row"><span>{t('登录时启动')}<small>{t('登录 Mac 后在后台启动，不打开窗口。')}</small></span><input type="checkbox" role="switch" aria-label={t('登录时启动')} checked={login?.state === 'enabled' || login?.state === 'approval'} disabled={!login || busy || login.state === 'unavailable'} onChange={event => void changeLogin(event.target.checked)} /></label>
   <LoginItemFeedback login={login} hintClass="settings-hint" errorClass="settings-error" openSettings={() => openLink('login-settings')} />
   <FanControl variant="settings" />
   {snapshot?.notebook === true && <>
    <label className="settings-row"><span>{t('合盖保持运行')}<small>{t('默认关闭 · 本次运行有效')}</small></span><input type="checkbox" role="switch" aria-label={t('合盖保持运行')} checked={sleep?.active ?? false} disabled={!sleep || busy} onChange={event => void changeSleep(event.target.checked)} /></label>
    {sleep?.active && <p className="settings-hint">{t('合盖后仍会运行，请注意耗电与散热，不要放入不通风的包内。')}</p>}
    {sleep?.error && <p role="alert" className="settings-error">{sleep.error}</p>}
   </>}
  </section>
  <LANAccessCard />
  <section className="settings-section" aria-label={t('软件更新')}>
   <div className="settings-row"><span>{t('软件更新')}<small>{releaseDisplayVersion(update?.current || import.meta.env.VITE_OPENSURGE_RELEASE_TAG)}</small></span><button disabled={!update || update.checking} onClick={() => { setUtilityError(''); void check() }}>{t(update?.checking ? '正在检查…' : '检查更新')}</button></div>
   {!update?.checking && (update?.failed ? <p role="status" className="settings-hint">{t('无法检查更新，请稍后重试。')}</p> : update?.version && update.url ? <p className="settings-hint"><button onClick={() => openLink('open-external', { url: update.url })}>{t('打开稳定版 {{version}} 下载页', { version: update.version })}</button></p> : update?.checked && <p role="status" className="settings-hint">{t('没有更新的稳定版')}</p>)}
  </section>
  <section className="settings-section settings-lifecycle" aria-label={t('退出与卸载')}>
   <h2>{t('退出与卸载')}</h2>
   <div className="settings-actions">
    <button disabled={busy} onClick={() => void quit(false)}>{t('只退出桌面 App…')}</button>
    <button disabled={busy || !canQuit} onClick={() => void quit(true)}>{t('退出 OpenSurge…')}</button>
   </div>
   <p className="settings-hint">{t('只退出桌面 App 时，后台服务与网关会继续运行。')}</p>
   {!canQuit && <p className="settings-hint">{t(snapshot?.status ? '请先在网络设置中停止网关并完成恢复。' : '状态暂不可用。后台连接恢复后会自动更新。')}</p>}
   <div className="settings-uninstall">
    <button className="settings-danger" disabled={busy || !canUninstall} onClick={() => void uninstall()}>{t('卸载 OpenSurge…')}</button>
    <p className="settings-hint">{t(state?.uninstall === 'preview' ? '预览版不会卸载正式安装的 OpenSurge。' : state?.uninstall === 'missing' ? '缺少可信的卸载组件，请重新安装当前版本。' : !canUninstall ? '请先在网络设置中停止网关，再卸载 OpenSurge。' : '卸载时可以选择保留配置与使用数据。')}</p>
   </div>
  </section>
  {!state && <p className="settings-hint" role="status">{t('正在读取 App 设置…')}</p>}
  {(error || utilityError || runtimeError) && <p role="alert" className="settings-error">{error || utilityError || runtimeError}</p>}
 </main>
}
