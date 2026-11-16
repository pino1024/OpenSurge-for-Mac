import { expect, it } from 'vitest'
import { fanSpeedRPM, movePoint } from './fanConfig'

const points = [{ c: 60, p: 0 }, { c: 72, p: 26 }, { c: 85, p: 100 }]

it('maps a stored percent onto this fan rpm range', () => {
  expect(Math.round(fanSpeedRPM(0, 1000, 4900))).toBe(1000)
  expect(Math.round(fanSpeedRPM(27, 1000, 4900))).toBe(2053)
  expect(Math.round(fanSpeedRPM(100, 1000, 4900))).toBe(4900)
})

it('keeps a dragged point between its neighbours', () => {
  expect(movePoint(points, 1, 90, 10)).toEqual([{ c: 60, p: 0 }, { c: 83, p: 10 }, { c: 85, p: 100 }])
  expect(movePoint(points, 0, 20, 40)).toEqual([{ c: 30, p: 26 }, { c: 72, p: 26 }, { c: 85, p: 100 }])
  expect(movePoint(points, 2, 120, 80)).toEqual([{ c: 60, p: 0 }, { c: 72, p: 26 }, { c: 105, p: 80 }])
})
