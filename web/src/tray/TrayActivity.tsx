import { useId, useState } from 'react'
import { DeviceAliasField } from '../components/DeviceAliasField'
import { connectionDeviceName, connectionOwnerKey } from '../connections'
import { t } from '../i18n'
import { buildSmoothChart } from '../trafficChart'
import { formatRate } from '../trafficFormat'
import { FanCurve } from './FanCurve'
import { saveTrayLayout, trayLayout } from './trayLayout'
import { useHostMeters, useTrayActivity } from './useTrayActivity'

export function TrayActivity({ gateway, onOpen }: { gateway?: string; onOpen: (page: string, options?: { owner?: string; section?: 'active-devices' }) => void }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const [devicesOpen, setDevicesOpen] = useState(() => trayLayout().devices)
  const [modeBusy, setModeBusy] = useState(false)
  const [modeError, setModeError] = useState('')
  const [aliasByIP, setAliasByIP] = useState<Record<string, string>>({})
  const detailID = useId()
  const toggle = (key: string) => setExpanded(current => current === key ? null : key)
  const toggleDevices = () => setDevicesOpen(current => { saveTrayLayout({ devices: !current }); return !current })
  const { traffic, proxy, history, deviceKeys, trafficFailed, proxyFailed, applyProxy } = useTrayActivity(gateway)
  const [chart, setChart] = useState<'cpu' | 'traffic' | 'fan'>(() => trayLayout().chart)
  const selectChart = (next: 'cpu' | 'traffic' | 'fan') => { setChart(next); saveTrayLayout({ chart: next }) }
  const meters = useHostMeters(chart === 'cpu' || chart === 'fan')
  const running = gateway === 'running' || gateway === 'degraded'
  const sampled = Boolean(traffic && history.length > 1)
  const maximum = Math.max(1, ...history.flatMap(point => [point.download, point.upload]))
  const download = buildSmoothChart(history.map(point => point.download), maximum, 4, 46)
  const upload = buildSmoothChart(history.map(point => point.upload), maximum, 4, 46)
  const setProxy = async (enabled: boolean) => {
    if (!proxy || modeBusy || proxy.enabled === enabled) return
    setModeBusy(true)
    setModeError('')
    try {
      const updated = await applyProxy(enabled)
      if (updated.warning) setModeError(updated.warning)
    } catch (cause) {
      setModeError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setModeBusy(false)
    }
  }
  const devices = traffic ? [traffic.gateway_local, ...deviceKeys.flatMap(key => traffic.devices.filter(device => connectionOwnerKey(device) === key))] : []
  const activeDevices = traffic?.devices.filter(device => device.active_connections > 0).length ?? 0
  const connections = traffic ? traffic.gateway_local.active_connections + traffic.totals.active_connections + (traffic.unclassified_connections ?? 0) : 0
  const cpuSegments = 14
  const chartTitle = chart === 'cpu' ? '本机状态' : chart === 'fan' ? '风扇调控' : '网络流量'
  const chartLabel = chart === 'cpu' ? '本机状态' : chart === 'fan' ? '风扇调控' : '经 OpenSurge 的实时流量'
  return <>
    <section className={`tray-traffic ${chart !== 'traffic' || (running && sampled) ? 'expanded' : ''}`} aria-label={t(chartLabel)}>
      <div className="tray-chart-header">
        <span>{t(chartTitle)}</span>
        <div className="tray-mode-switch tray-chart-switch" role="group" aria-label={t('图表内容')}>
          <button type="button" aria-pressed={chart === 'cpu'} onClick={() => selectChart('cpu')}>CPU</button>
          <button type="button" aria-pressed={chart === 'traffic'} onClick={() => selectChart('traffic')}>{t('流量')}</button>
          <button type="button" aria-pressed={chart === 'fan'} onClick={() => selectChart('fan')}>{t('风扇')}</button>
        </div>
      </div>
      {chart === 'fan' ? <FanCurve meters={meters} /> : chart === 'cpu' ? <div className="tray-cpu">
        <div className="tray-rates tray-rates-host">
          <div><span>CPU</span><strong>{meters.cpu_ok ? `${Math.round(meters.cpu)}%` : '—'}</strong></div>
          <div><span>{t('内存')}</span><strong>{meters.memory_ok ? `${Math.round(meters.memory)}%` : '—'}</strong></div>
          <div><span>{t('磁盘')}</span><strong>{meters.disk_ok ? `${Math.round(meters.disk)}%` : '—'}</strong></div>
        </div>
        <div className="cpu-bars" role="img" aria-label={t('每个核的当前占用')}>
          {meters.cores.map((value, index) => {
            const lit = Math.round(Math.min(100, Math.max(0, value)) / 100 * cpuSegments)
            const temp = meters.core_temps[index]
            const tempLabel = typeof temp === 'number' && temp > 0 ? `${Math.round(temp)}°` : ''
            return <div className="cpu-bar" key={index} title={`${index + 1} · ${Math.round(value)}%${tempLabel ? ` · ${Math.round(temp)}°C` : ''}`}>
              <div className="cpu-segs">{Array.from({ length: cpuSegments }, (_, segment) => <span key={segment} className={segment < lit ? 'cpu-seg on' : 'cpu-seg'} />)}</div>
              {tempLabel && <span className="cpu-temp">{tempLabel}</span>}
            </div>
          })}
        </div>
        {meters.cores.length === 0 && <p className="tray-caption" role="status">{t('正在采样 CPU…')}</p>}
        {(meters.power_temp > 0 || meters.gpu_temp > 0 || meters.perf_temp > 0) && <div className="cpu-groups">
          {([[t('供电'), meters.power_temp], ['GPU', meters.gpu_temp], ['CPU', meters.perf_temp]] as const).map(([name, value]) => <span key={name}>{name}<b>{value > 0 ? `${Math.round(value)}°` : '—'}</b></span>)}
        </div>}
      </div> : <>
      <div className="tray-traffic-reveal" aria-hidden={!running || !sampled}><div className="tray-detail-inner"><div className="tray-rates">
        <div><span><i className="download-key" />{t('下载')}</span><strong>{sampled ? formatRate(traffic!.gateway_rates.download) : '—'}</strong></div>
        <div><span><i className="upload-key" />{t('上传')}</span><strong>{sampled ? formatRate(traffic!.gateway_rates.upload) : '—'}</strong></div>
      </div>
      <svg className="tray-chart" viewBox="0 0 100 50" preserveAspectRatio="none" aria-hidden="true">
        <path className="chart-baseline" d="M 0 46 H 100" />
        {sampled && <><path className="download-area" d={download.areaPath} /><path className="download-line" d={download.linePath} /><path className="upload-line" d={upload.linePath} /></>}
      </svg></div></div>
      <div className="tray-traffic-caption"><p className="tray-caption" role="status">{t(gateway === 'stopped' ? '启动网关后显示实时流量' : !running ? '等待网关状态…' : trafficFailed ? '流量暂不可用' : !sampled ? '正在采样流量…' : '近 60 秒 · 经 OpenSurge 的流量')}</p>{gateway === 'stopped' && <button className="tray-text-button" onClick={() => onOpen('network')}>{t('前往启动')} <span aria-hidden="true">›</span></button>}</div>
      </>}
    </section>
    <div className="tray-routing-group">
      <div className="tray-routing-header">
        <span>{t('代理')}{!proxy && <small>{t(proxyFailed ? '暂不可用' : '正在读取…')}</small>}</span>
        <div className="tray-mode-switch" role="group" aria-label={t('代理')}>
          <button type="button" aria-pressed={proxy ? proxy.enabled : false} disabled={!proxy || modeBusy} onClick={() => void setProxy(true)}>{t('开启')}</button>
          <button type="button" aria-pressed={proxy ? !proxy.enabled : false} disabled={!proxy || modeBusy} onClick={() => void setProxy(false)}>{t('关闭')}</button>
        </div>
      </div>
      {modeError && <p className="tray-error" role="alert">{modeError}</p>}
      <p className="tray-hint tray-proxy-hint">{t('关闭后本机和下游都直连上网，不走梯子。网关保持运行。')}</p>
    </div>
    <section className={`tray-devices ${devicesOpen ? 'expanded' : ''}`} aria-label={t('活跃设备')}>
      <div className="tray-section-heading">
        <button type="button" className="tray-network-toggle" aria-expanded={devicesOpen} aria-controls="tray-device-list" onClick={toggleDevices}><h2>{t('活跃设备')}</h2><span className="tray-caption">{t(devicesOpen ? '收起详情' : '查看详情')} <svg className="tray-disclosure" viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2 4 4-4 4" /></svg></span></button>
      </div>
      <div className="tray-network-reveal" id="tray-device-list" aria-hidden={!devicesOpen} inert={!devicesOpen}><div className="tray-network-inner">
      {devices.length ? <div className="tray-device-list">{devices.map((device, index) => {
        const key = connectionOwnerKey(device)
        const name = index === 0 ? t('本机 Mac') : Object.hasOwn(aliasByIP, device.ip) ? aliasByIP[device.ip] || connectionDeviceName({ ...device, name: '' }) : connectionDeviceName(device)
        const open = expanded === key
        return <div className={`tray-device-group ${open ? 'expanded' : ''}`} key={key}><button className="tray-device" onClick={() => toggle(key)} aria-expanded={open} aria-controls={`${detailID}-${index}`} aria-label={t('设备详情：{{name}}', { name })}>
        <span className={`tray-device-glyph ${index === 0 ? 'mac' : 'device'}`} aria-hidden="true"><svg viewBox="0 0 20 20"><rect x={index === 0 ? 2 : 5} y="3" width={index === 0 ? 16 : 10} height="12" rx="2" /><path d={index === 0 ? 'M 7 18 H 13 M 10 15 V 18' : 'M 9 13 H 11'} /></svg></span>
        <span className="tray-device-name">{name}</span><span className="tray-device-rate">↓ {sampled ? formatRate(device.download_rate) : '—'}</span><svg className="tray-disclosure" viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2 4 4-4 4" /></svg>
      </button><div className="tray-detail-reveal" id={`${detailID}-${index}`} aria-hidden={!open} inert={!open}><div className="tray-detail-inner"><div className="tray-device-details">
        <dl className="tray-status">
          <div><dt>{t('设备地址')}</dt><dd title={device.addresses?.join(' · ') || device.ip}>{device.addresses?.join(' · ') || device.ip || '—'}</dd></div>
          <div><dt>{t('活跃连接')}</dt><dd>{device.active_connections}</dd></div>
          <div><dt>{t('上传')}</dt><dd>{sampled ? formatRate(device.upload_rate) : '—'}</dd></div>
          <div><dt>{t('下载')}</dt><dd>{sampled ? formatRate(device.download_rate) : '—'}</dd></div>
          {device.primary_egress && <div><dt>{t('当前出口')}</dt><dd title={device.primary_egress}>{device.primary_egress}</dd></div>}
          {index > 0 && device.ip ? <div><dt>{t('别名')}</dt><dd><DeviceAliasField ip={device.ip} currentName={aliasByIP[device.ip] ?? device.name} onSaved={alias => setAliasByIP(current => ({ ...current, [device.ip]: alias }))} /></dd></div> : null}
        </dl>
        <button className="tray-text-button" onClick={() => onOpen('connections', { owner: key })} aria-label={t('查看 {{name}} 的连接', { name })}>{t('在桌面窗口中查看连接')} <span aria-hidden="true">↗</span></button>
      </div></div></div></div>
      })}</div> : <p className="tray-empty">{t(!running ? '启动网关后显示活跃设备' : trafficFailed ? '设备流量暂不可用' : '正在读取活跃设备…')}</p>}
      <div className="tray-device-footer"><p className="tray-caption">{traffic ? t('活跃下游 {{devices}} 台 · 总连接 {{connections}}', { devices: activeDevices, connections }) : t('本机 Mac 与下游设备分别统计')}</p><button className="tray-text-button" onClick={() => onOpen('dashboard', { section: 'active-devices' })}>{t('查看全部')} <span aria-hidden="true">›</span></button></div>
      </div></div>
    </section>
  </>
}
