const storageKey = 'opensurge-tray-layout'

export type TrayChart = 'cpu' | 'traffic' | 'fan'

export type TrayLayout = {
  chart: TrayChart
  devices: boolean
  preferences: boolean
  network: boolean
  routing: boolean
}

const defaults: TrayLayout = { chart: 'cpu', devices: false, preferences: false, network: false, routing: false }

export function trayLayout(): TrayLayout {
  try {
    const raw = JSON.parse(localStorage.getItem(storageKey) || '') as Partial<TrayLayout>
    return {
      chart: raw.chart === 'traffic' || raw.chart === 'fan' ? raw.chart : 'cpu',
      devices: raw.devices === true,
      preferences: raw.preferences === true,
      network: raw.network === true,
      routing: raw.routing === true,
    }
  } catch {
    return { ...defaults }
  }
}

export function saveTrayLayout(patch: Partial<TrayLayout>) {
  localStorage.setItem(storageKey, JSON.stringify({ ...trayLayout(), ...patch }))
}
