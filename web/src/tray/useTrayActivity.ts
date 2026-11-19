import { useEffect, useState } from 'react'
import { api } from '../api'
import { connectionOwnerKey } from '../connections'
import { desktopAction } from '../desktop'
import type { DeviceTraffic, ProxyEnabled } from '../types'
import { watchVisibleRefresh } from '../visibility'

export type HostMeters = { download: number; download_ok: boolean; cpu: number; cpu_ok: boolean; memory: number; memory_ok: boolean; disk: number; disk_ok: boolean; temperature: number; temperature_ok: boolean; fan_rpm: number; fan_ok: boolean; fan_visible: boolean; fan_min: number; fan_max: number; control_temp: number; control_percent: number; control_ok: boolean; control_active: boolean; cores: number[]; core_temps: number[]; power_temp: number; gpu_temp: number; perf_temp: number }
const emptyMeters: HostMeters = { download: 0, download_ok: false, cpu: 0, cpu_ok: false, memory: 0, memory_ok: false, disk: 0, disk_ok: false, temperature: 0, temperature_ok: false, fan_rpm: 0, fan_ok: false, fan_visible: false, fan_min: 0, fan_max: 0, control_temp: 0, control_percent: 0, control_ok: false, control_active: false, cores: [], core_temps: [], power_temp: 0, gpu_temp: 0, perf_temp: 0 }

export function useHostMeters(enabled: boolean): HostMeters {
  const [meters, setMeters] = useState(emptyMeters)
  useEffect(() => {
    if (!enabled) return
    let active = true
    const stop = watchVisibleRefresh(async () => {
      try {
        const next = await desktopAction<HostMeters>('host-meters')
        if (active && Array.isArray(next?.cores)) setMeters({ ...emptyMeters, ...next, cores: next.cores, core_temps: Array.isArray(next.core_temps) ? next.core_temps : [] })
      } catch { /* keep the last sample */ }
    }, 1_000)
    return () => { active = false; stop() }
  }, [enabled])
  return meters
}

export type TrayTrafficPoint = { time: number; upload: number; download: number }
export type TrayTrafficSnapshot = { traffic: DeviceTraffic | null; history: TrayTrafficPoint[] | null; failed: boolean }
type Activity = { traffic: DeviceTraffic | null; proxy: ProxyEnabled | null; history: TrayTrafficPoint[]; deviceKeys: string[]; trafficFailed: boolean; proxyFailed: boolean }
const empty: Activity = { traffic: null, proxy: null, history: [], deviceKeys: [], trafficFailed: false, proxyFailed: false }

// Native code keeps a bounded observation cache while WebViews sleep. The popup
// only reads that cache while visible; slow routing reads cannot stall the chart.
export function useTrayActivity(gateway?: string) {
  const [activity, setActivity] = useState<Activity>(empty)
  const running = gateway === 'running' || gateway === 'degraded'
  useEffect(() => {
    setActivity(empty)
    let active = true
    let rankedAt = 0
    const stopProxy = watchVisibleRefresh(async () => {
      try {
        const proxy = await api.proxyEnabled()
        if (active) setActivity(current => ({ ...current, proxy, proxyFailed: false }))
      } catch {
        if (active) setActivity(current => ({ ...current, proxy: null, proxyFailed: true }))
      }
    }, 2_000)
    if (!running) return () => { active = false; stopProxy() }
    const stopTraffic = watchVisibleRefresh(async () => {
      let snapshot: TrayTrafficSnapshot
      try { snapshot = await desktopAction<TrayTrafficSnapshot>('tray-activity') }
      catch { snapshot = { traffic: null, history: [], failed: true } }
      if (!active) return
      const next = snapshot.traffic
      const sampledAt = next ? Date.parse(next.sampled_at) : NaN
      const now = Number.isFinite(sampledAt) ? sampledAt : Date.now()
      const rerank = now - rankedAt >= 10_000 || !rankedAt
      if (rerank) rankedAt = now
      setActivity(current => {
        const candidates = next?.devices.filter(device => device.active_connections > 0) ?? []
        const ranked = [...candidates].sort((a, b) => (b.download_rate + b.upload_rate) - (a.download_rate + a.upload_rate) || connectionOwnerKey(a).localeCompare(connectionOwnerKey(b))).map(connectionOwnerKey)
        const retained = current.deviceKeys.filter(key => ranked.includes(key))
        const deviceKeys = (rerank ? ranked : [...retained, ...ranked.filter(key => !retained.includes(key))]).slice(0, 2)
        return { ...current, traffic: next, history: snapshot.history ?? [], deviceKeys, trafficFailed: snapshot.failed }
      })
    }, 1_000)
    return () => { active = false; stopTraffic(); stopProxy() }
  }, [running])

  const applyProxy = async (enabled: boolean) => {
    const updated = await api.setProxyEnabled(enabled)
    setActivity(current => ({ ...current, proxy: updated, proxyFailed: false }))
    return updated
  }

  return { ...activity, traffic: running ? activity.traffic : null, history: running ? activity.history : [], deviceKeys: running ? activity.deviceKeys : [], trafficFailed: running ? activity.trafficFailed : false, applyProxy }
}
