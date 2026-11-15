// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../api'
import { activateLanguage, prepareLanguage } from '../i18n'
import { DeviceAliasField } from './DeviceAliasField'

vi.mock('../api', () => ({ api: { setUIPreferences: vi.fn() } }))

beforeEach(async () => {
  await prepareLanguage('en')
  activateLanguage('en')
  vi.mocked(api.setUIPreferences).mockResolvedValue({ schema_version: 1, language: 'en', device_aliases: { '192.168.31.63': 'Win PC' } })
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
  activateLanguage('zh-Hans')
})

it('saves a display alias for a connected IP without changing routing', async () => {
  const onSaved = vi.fn()
  render(<DeviceAliasField ip="192.168.31.63" onSaved={onSaved} />)
  await userEvent.click(screen.getByRole('button', { name: 'Set a display alias for 192.168.31.63' }))
  await userEvent.type(screen.getByRole('textbox', { name: 'Set a display alias for 192.168.31.63' }), 'Win PC')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(api.setUIPreferences).toHaveBeenCalledWith({ device_aliases: { '192.168.31.63': 'Win PC' } })
  expect(onSaved).toHaveBeenCalledWith('Win PC')
})

it('clears a saved alias', async () => {
  render(<DeviceAliasField ip="192.168.31.63" currentName="Win PC" />)
  await userEvent.click(screen.getByRole('button', { name: 'Set a display alias for 192.168.31.63' }))
  await userEvent.click(screen.getByRole('button', { name: 'Clear' }))
  expect(api.setUIPreferences).toHaveBeenCalledWith({ device_aliases: { '192.168.31.63': '' } })
})
