import { t } from '../i18n'
import { useFanConfig } from './fanConfig'

export function FanControl({ variant = 'tray' }: { variant?: 'tray' | 'settings' }) {
  const { config, error, busy, save } = useFanConfig()
  const rowClass = variant === 'settings' ? 'settings-row' : 'fan-row'
  return <div className={variant === 'settings' ? 'settings-fan' : 'tray-fan'}>
    <div className={rowClass}>
      <span>{t('风扇转速')}</span>
      <input type="checkbox" role="switch" aria-label={t('风扇转速')} checked={config.visible} disabled={busy} onChange={event => void save({ ...config, visible: event.target.checked })} />
    </div>
    {error && <p role="alert" className={variant === 'settings' ? 'settings-error' : 'tray-error'}>{error}</p>}
  </div>
}
