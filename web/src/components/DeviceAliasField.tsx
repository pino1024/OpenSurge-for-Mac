import { useEffect, useState } from 'react'
import { api } from '../api'
import { t } from '../i18n'

const maxAliasLength = 40

export function DeviceAliasField({
  ip,
  currentName,
  compact = false,
  onSaved,
}: {
  ip: string
  currentName?: string
  compact?: boolean
  onSaved?: (alias: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(currentName ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!editing) setValue(currentName ?? '')
  }, [currentName, editing])

  const save = async (next = value) => {
    const alias = next.trim()
    if (alias.length > maxAliasLength) {
      setError(t('别名最多 40 个字符'))
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.setUIPreferences({ device_aliases: { [ip]: alias } })
      onSaved?.(alias)
      setEditing(false)
      setValue(alias)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  if (!editing) {
    return <button className={compact ? 'traffic-alias' : 'tray-text-button'} type="button" disabled={busy} aria-label={t('给 {{ip}} 设置显示别名', { ip })} onClick={event => { event.stopPropagation(); setEditing(true); setError('') }}>
      {t(currentName ? '改别名' : '设置别名')}
    </button>
  }

  return <span className={`device-alias-editor ${compact ? 'compact' : ''}`} onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
    <input
      aria-label={t('给 {{ip}} 设置显示别名', { ip })}
      autoFocus
      disabled={busy}
      maxLength={maxAliasLength}
      placeholder={ip}
      value={value}
      onChange={event => setValue(event.target.value)}
      onKeyDown={event => {
        if (event.key === 'Enter') {
          event.preventDefault()
          void save()
        }
        if (event.key === 'Escape') {
          event.preventDefault()
          setEditing(false)
          setError('')
          setValue(currentName ?? '')
        }
      }}
    />
    <button type="button" disabled={busy} onClick={() => void save()}>{t('保存')}</button>
    <button type="button" disabled={busy} onClick={() => void save('')}>{t('清除')}</button>
    {error && <small className="field-error" role="alert">{error}</small>}
  </span>
}
