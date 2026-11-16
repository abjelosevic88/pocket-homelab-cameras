// Numbers: what the cameras saw day by day, the busy hours, each camera, false alarms and storage.
import { useEffect, useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import { LABELS } from '../lib/cams'
import { fmtBytes, fmtDate } from '../lib/format'
import { Loading, Segmented } from '../components/ui'
import { Icon } from '../components/icons'
import { HBars, useWidth } from '../components/Chart'

interface DayRow { day: string; person: number; vehicle: number; animal: number; parcel: number; other: number; nothing: number; alerts: number; doorbell: number }
interface Stats { days: DayRow[]; hours: number[][]; cameras: { id: number; name: string; events: number; nothing: number; bytes: number }[]; totals: Record<string, number>; storage_bytes: number; storage_cap: number }

const SERIES: { key: keyof DayRow; label: string; color: string }[] = [
  { key: 'person', label: 'People', color: 'var(--c-person)' },
  { key: 'parcel', label: 'Parcels', color: 'var(--c-parcel)' },
  { key: 'vehicle', label: 'Vehicles', color: 'var(--c-vehicle)' },
  { key: 'animal', label: 'Animals', color: 'var(--c-animal)' },
  { key: 'doorbell', label: 'Doorbell', color: 'var(--c-doorbell)' },
  { key: 'other', label: 'Movement', color: 'var(--c-motion)' },
]

/** Events a day, stacked by what they were — drawn at the box's real size. */
function DayBars({ days }: { days: DayRow[] }) {
  const [ref, W] = useWidth(640)
  const H = 190, L = 28, R = 6, T = 10, B = 24
  const totals = days.map(d => SERIES.reduce((s, x) => s + (d[x.key] as number), 0))
  const max = Math.max(1, ...totals)
  const step = Math.ceil(max / 4)
  const top = step * 4
  const cw = (W - L - R) / Math.max(1, days.length)
  const bw = Math.max(2, Math.min(18, cw - 3))
  const y = (v: number) => T + (H - T - B) * (1 - v / top)
  const every = Math.max(1, Math.ceil(days.length / Math.max(1, Math.floor((W - L - R) / 56))))
  const [hover, setHover] = useState(-1)
  return (
    <div className="chart-box">
      <div ref={ref}><svg viewBox={`0 0 ${W} ${H}`} width={W} height={H} onMouseLeave={() => setHover(-1)}>
        {[0, 1, 2, 3, 4].map(i => <g key={i}><line x1={L} x2={W - R} y1={y(step * i)} y2={y(step * i)} className="grid-line" /><text x={L - 6} y={y(step * i) + 4} textAnchor="end" className="axis">{step * i}</text></g>)}
        {days.map((d, i) => {
          let acc = 0
          const x = L + i * cw + (cw - bw) / 2
          return (
            <g key={d.day} onMouseEnter={() => setHover(i)}>
              <rect x={L + i * cw} y={T} width={cw} height={H - T - B} fill="transparent" />
              {SERIES.map(sv => {
                const v = d[sv.key] as number
                if (!v) return null
                const y0 = y(acc), y1 = y(acc + v)
                acc += v
                return <rect key={sv.key} x={x} y={y1} width={bw} height={Math.max(1, y0 - y1)} fill={sv.color} rx={bw > 6 ? 2 : 0} opacity={hover === -1 || hover === i ? 1 : 0.45} />
              })}
              {i % every === 0 && <text x={L + i * cw + cw / 2} y={H - 6} textAnchor="middle" className="axis">{Number(d.day.slice(8))}.{Number(d.day.slice(5, 7))}.</text>}
            </g>
          )
        })}
      </svg></div>
      {hover >= 0 && <div className="chart-readout small">
        <b>{fmtDate(days[hover].day)}</b>: {totals[hover] ? SERIES.filter(sv => days[hover][sv.key]).map(sv => `${days[hover][sv.key]} ${sv.label.toLowerCase()}`).join(', ') : 'nothing'}
        {days[hover].nothing ? ` · ${days[hover].nothing} false alarms` : ''}{days[hover].alerts ? ` · ${days[hover].alerts} alerts` : ''}
      </div>}
      <div className="legend small muted">{SERIES.map(sv => <span key={sv.key}><i className="lg" style={{ background: sv.color }} />{sv.label}</span>)}</div>
    </div>
  )
}

const WD = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

/** Which hours are busy, by weekday. */
function Hours({ hours }: { hours: number[][] }) {
  const max = Math.max(1, ...hours.flat())
  return (
    <div className="hours-grid">
      <span />
      {Array.from({ length: 24 }, (_, h) => <span key={h} className="hours-h">{h % 3 === 0 ? String(h).padStart(2, '0') : ''}</span>)}
      {hours.map((row, d) => [
        <span key={`l${d}`} className="hours-d">{WD[d]}</span>,
        ...row.map((n, h) => <span key={`${d}-${h}`} className="hours-cell" style={{ opacity: n ? 0.15 + 0.85 * (n / max) : 1 }} data-empty={n ? undefined : ''} title={`${WD[d]} ${String(h).padStart(2, '0')}:00 — ${n} ${n === 1 ? 'event' : 'events'}`} />),
      ])}
    </div>
  )
}

export default function StatsPage() {
  const [days, setDays] = useState(30)
  const [s, setS] = useState<Stats | null>(null)
  useEffect(() => { setS(null); api.get<Stats>(`${V1}/stats?days=${days}`).then(setS).catch(() => {}) }, [days])
  const falsePct = useMemo(() => s ? Math.round((100 * s.totals.nothing) / Math.max(1, s.totals.nothing + s.totals.events)) : 0, [s])
  return (
    <div className="stack loose">
      <div className="row"><Segmented size="sm" value={days} onChange={setDays} options={[{ value: 7, label: '7 days' }, { value: 30, label: '30 days' }, { value: 90, label: '90 days' }]} /></div>
      {!s ? <Loading /> : <>
        <div className="set-stats">
          <div className="set-stat"><Icon name="bell" size={16} /><span className="set-stat-n tnum">{s.totals.events.toLocaleString()}</span><span className="set-stat-l">events</span></div>
          {LABELS.map(l => <div key={l.id} className="set-stat"><Icon name={l.icon} size={16} /><span className="set-stat-n tnum">{(s.totals[l.id] || 0).toLocaleString()}</span><span className="set-stat-l">{l.many.toLowerCase()}</span></div>)}
          <div className="set-stat"><Icon name="smartphone" size={16} /><span className="set-stat-n tnum">{s.totals.alerts.toLocaleString()}</span><span className="set-stat-l">alerts sent</span></div>
          <div className="set-stat"><Icon name="x-circle" size={16} /><span className="set-stat-n tnum">{falsePct}%</span><span className="set-stat-l">false alarms</span></div>
        </div>
        <section className="card"><div className="card-head"><h3>Day by day</h3></div><DayBars days={s.days} /></section>
        <section className="card"><div className="card-head"><h3>Busy hours</h3><span className="muted small">people, cars, parcels and animals by hour of the week</span></div><Hours hours={s.hours} /></section>
        <div className="cols2">
          <section className="card"><div className="card-head"><h3>Each camera</h3></div>
            <HBars items={s.cameras.map(c => ({ label: c.name, value: c.events, hint: c.nothing ? `${c.nothing} false alarms` : undefined }))} format={v => String(Math.round(v))} />
          </section>
          <section className="card"><div className="card-head"><h3>Storage</h3><span className="muted small">{fmtBytes(s.storage_bytes)}{s.storage_cap ? ` of ${fmtBytes(s.storage_cap)}` : ''}</span></div>
            {s.storage_bytes ? <HBars items={s.cameras.filter(c => c.bytes).map(c => ({ label: c.name, value: c.bytes }))} format={fmtBytes} /> : <p className="muted small">Nothing recorded yet. Cameras with video (RTSP) record around events or all the time; the others keep pictures of their events.</p>}
          </section>
        </div>
      </>}
    </div>
  )
}
