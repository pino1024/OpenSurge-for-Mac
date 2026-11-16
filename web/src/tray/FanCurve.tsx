import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { t } from '../i18n'
import type { HostMeters } from './useTrayActivity'
import { fanSpeedRPM, fanTempMax, fanTempMin, movePoint, useFanConfig, type FanPoint } from './fanConfig'

const width = 320
const height = 132
const pad = { l: 36, r: 10, t: 14, b: 22 }

function xOf(c: number) {
  return pad.l + (c - fanTempMin) / (fanTempMax - fanTempMin) * (width - pad.l - pad.r)
}

function yOf(p: number) {
  return pad.t + (1 - p / 100) * (height - pad.t - pad.b)
}

function pointAt(svg: SVGSVGElement, event: PointerEvent<SVGCircleElement>) {
  const matrix = svg.getScreenCTM()
  if (!matrix) return null
  const local = svg.createSVGPoint()
  local.x = event.clientX
  local.y = event.clientY
  const mapped = local.matrixTransform(matrix.inverse())
  const c = fanTempMin + (mapped.x - pad.l) / (width - pad.l - pad.r) * (fanTempMax - fanTempMin)
  const p = (1 - (mapped.y - pad.t) / (height - pad.t - pad.b)) * 100
  return { c, p }
}

export function FanCurve({ meters }: { meters: HostMeters }) {
  const { config, error, busy, save } = useFanConfig()
  const [draft, setDraft] = useState<FanPoint[] | null>(null)
  const [selected, setSelected] = useState<number | null>(null)
  const drag = useRef<number | null>(null)
  const points = draft ?? config.points
  const range = meters.fan_max > meters.fan_min && meters.fan_min >= 0 ? { min: meters.fan_min, max: meters.fan_max } : null
  const speedLabel = (percent: number) => range ? String(Math.round(fanSpeedRPM(percent, range.min, range.max))) : ''
  useEffect(() => {
    if (drag.current === null) setDraft(null)
  }, [config])
  const svgRef = useRef<SVGSVGElement>(null)
  const latest = useRef(points)
  latest.current = points
  const line = [`M ${xOf(fanTempMin).toFixed(1)} ${yOf(points[0]?.p ?? 0).toFixed(1)}`, ...points.map(point => `L ${xOf(point.c).toFixed(1)} ${yOf(point.p).toFixed(1)}`), `L ${xOf(fanTempMax).toFixed(1)} ${yOf(points.at(-1)?.p ?? 0).toFixed(1)}`].join(' ')
  const caption = meters.control_ok && meters.control_temp > 0
    ? `${t('控制温度')} ${Math.round(meters.control_temp)}°C${meters.fan_visible && meters.fan_ok ? ` · ${Math.round(meters.fan_rpm)} rpm` : ''}`
    : t('正在采样风扇…')
  const chosen = selected != null ? points[selected] : null
  const readout = chosen ? (range ? `${chosen.c}°C · ${speedLabel(chosen.p)} rpm` : `${chosen.c}°C · ${chosen.p}%`) : ''
  const readoutX = chosen ? Math.min(width - 58, Math.max(pad.l + 34, xOf(chosen.c))) : 0
  const readoutAbove = chosen ? yOf(chosen.p) > 40 : false
  const readoutY = chosen ? (readoutAbove ? yOf(chosen.p) - 16 : yOf(chosen.p) + 22) : 0
  const readoutWidth = readout.length * 6.4 + 14
  return <div className="fan-curve">
    <div className="fan-row">
      <span>{t('策略生效')}<small>{t('关闭后交回系统。')}</small></span>
      <input type="checkbox" role="switch" aria-label={t('策略生效')} checked={config.enabled} disabled={busy} onChange={event => void save({ ...config, enabled: event.target.checked })} />
    </div>
    <svg ref={svgRef} className="fan-curve-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={t('风扇调控')} onPointerDown={event => { if (!(event.target as Element).classList.contains('fan-handle')) setSelected(null) }}>
      {[0, 50, 100].map(percent => <g key={percent}>
        <line className="fan-curve-grid" x1={pad.l} x2={width - pad.r} y1={yOf(percent)} y2={yOf(percent)} />
        {range && <text className="fan-curve-label" x={pad.l - 4} y={yOf(percent) + 3} textAnchor="end">{speedLabel(percent)}</text>}
      </g>)}
      {[40, 60, 80, 100].map(temp => <text key={temp} className="fan-curve-label" x={xOf(temp)} y={height - 6} textAnchor="middle">{temp}</text>)}
      <path className="fan-curve-line" d={line} />
      {meters.control_ok && meters.control_temp > 0 && <circle className="fan-live" cx={xOf(Math.min(fanTempMax, Math.max(fanTempMin, meters.control_temp)))} cy={yOf(Math.min(100, Math.max(0, meters.control_percent)))} r={4} />}
      {points.map((point, index) => <circle key={index} className={selected === index ? 'fan-handle selected' : 'fan-handle'} cx={xOf(point.c)} cy={yOf(point.p)} r={7} role="slider" tabIndex={0} aria-label={t('曲线点 {{n}}', { n: index + 1 })} aria-valuemin={fanTempMin} aria-valuemax={fanTempMax} aria-valuenow={point.c} aria-valuetext={range ? `${point.c}°C ${speedLabel(point.p)} rpm` : `${point.c}°C`}
        onPointerDown={event => { drag.current = index; setSelected(index); event.stopPropagation(); event.currentTarget.setPointerCapture?.(event.pointerId); setDraft(points) }}
        onFocus={() => setSelected(index)}
        onPointerMove={event => {
          if (drag.current !== index || !svgRef.current) return
          const next = pointAt(svgRef.current, event)
          if (!next) return
          const moved = movePoint(latest.current, index, next.c, next.p)
          latest.current = moved
          setDraft(moved)
        }}
        onPointerUp={() => { if (drag.current !== index) return; drag.current = null; void save({ ...config, points: latest.current }) }}
        onKeyDown={event => {
          const step = event.shiftKey ? 5 : 1
          const next = { c: point.c, p: point.p }
          if (event.key === 'ArrowLeft') next.c -= step
          else if (event.key === 'ArrowRight') next.c += step
          else if (event.key === 'ArrowUp') next.p += step
          else if (event.key === 'ArrowDown') next.p -= step
          else return
          event.preventDefault()
          const moved = movePoint(points, index, next.c, next.p)
          setDraft(moved)
          void save({ ...config, points: moved })
        }}
      />)}
      {chosen && <>
        <rect className="fan-point-chip" x={readoutX - readoutWidth / 2} y={readoutY - 11} width={readoutWidth} height={15} rx={4} />
        <text className="fan-point-value" x={readoutX} y={readoutY} textAnchor="middle">{readout}</text>
      </>}
    </svg>
    <p className="tray-caption" role="status">{caption} · {t('拖动圆点调整风扇曲线。')}</p>
    {error && <p role="alert" className="tray-error">{error}</p>}
  </div>
}
