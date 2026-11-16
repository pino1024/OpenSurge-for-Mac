import { useEffect, useState } from 'react'
import { desktopAction } from '../desktop'

export type FanPoint = { c: number; p: number }
export type FanConfig = { visible: boolean; enabled: boolean; points: FanPoint[] }

export const fanTempMin = 30
export const fanTempMax = 105

export function fanSpeedRPM(percent: number, min: number, max: number) {
  return min + (max - min) * percent / 100
}

export function movePoint(points: FanPoint[], index: number, c: number, p: number): FanPoint[] {
  const next = points.map(point => ({ ...point }))
  const point = next[index]
  if (!point) return next
  const minC = index > 0 ? next[index - 1].c + 2 : fanTempMin
  const maxC = index < next.length - 1 ? next[index + 1].c - 2 : fanTempMax
  const minP = index > 0 ? next[index - 1].p : 0
  const maxP = index < next.length - 1 ? next[index + 1].p : 100
  point.c = Math.round(Math.min(maxC, Math.max(minC, c)))
  point.p = Math.round(Math.min(maxP, Math.max(minP, p)))
  return next
}

export function defaultFanPoints(): FanPoint[] {
  return [{ c: 60, p: 0 }, { c: 72, p: 26 }, { c: 85, p: 100 }]
}

export function defaultFanConfig(): FanConfig {
  return { visible: true, enabled: true, points: defaultFanPoints() }
}

let current = defaultFanConfig()
const listeners = new Set<(config: FanConfig) => void>()

export function fanConfig() {
  return current
}

export function resetFanConfig() {
  current = defaultFanConfig()
  listeners.forEach(listener => listener(current))
}

export function replaceFan(config: FanConfig) {
  current = config
  listeners.forEach(listener => listener(config))
}

export function subscribeFan(listener: (config: FanConfig) => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

export function isFanConfig(value: Partial<FanConfig> | undefined): value is FanConfig {
  return typeof value?.visible === 'boolean' && typeof value.enabled === 'boolean' && Array.isArray(value.points) && value.points.length >= 2 && value.points.every(point => typeof point?.c === 'number' && typeof point.p === 'number')
}

export async function persistFan(next: FanConfig): Promise<FanConfig> {
  const saved = await desktopAction<Partial<FanConfig>>('fan-control', { fan_visible: next.visible, fan_enabled: next.enabled, fan_points: next.points })
  const applied = isFanConfig(saved) ? saved : next
  replaceFan(applied)
  return applied
}

export function useFanConfig() {
  const [config, setConfig] = useState(fanConfig)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => subscribeFan(setConfig), [])
  useEffect(() => {
    let active = true
    desktopAction<Partial<FanConfig>>('fan-control').then(next => {
      if (active && isFanConfig(next)) replaceFan(next)
    }).catch(() => {})
    return () => { active = false }
  }, [])
  const save = async (next: FanConfig) => {
    setBusy(true)
    setError('')
    try {
      await persistFan(next)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }
  return { config, error, busy, save }
}
