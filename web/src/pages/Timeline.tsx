// Timeline: one day of every camera — what was recorded, the events, the mode — and the video.
import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { MODE } from '../lib/cams'
import type { CamEvent, Timeline } from '../lib/types'
import { addDays, fmtDate, fmtTime, todayISO, weekday } from '../lib/format'
import { Empty, Loading } from '../components/ui'
import { Icon } from '../components/icons'
import { DayBar, Player } from '../components/Player'

export default function TimelinePage() {
  const navigate = useNavigate()
  const [day, setDay] = useState(todayISO())
  const [tl, setTl] = useState<Timeline | null>(null)
  const [sel, setSel] = useState<{ cam: number; t: number } | null>(null)
  const [at, setAt] = useState<number | null>(null)
  useEffect(() => { setTl(null); api.get<Timeline>(`${V1}/timeline?day=${day}`).then(setTl).catch(() => {}) }, [day])
  const go = (d: string) => { setDay(d); setSel(null); setAt(null) }
  const row = sel && tl?.rows.find(r => r.camera.id === sel.cam)
  const pick = (cam: number, kind: string, hasVideo: boolean, t: number, e?: CamEvent) => {
    if (kind === 'rtsp' && hasVideo) setSel({ cam, t })
    else if (e) navigate(`/events/${e.id}`)
  }
  return (
    <div className="timeline-page stack">
      <div className="row day-nav">
        <button type="button" className="btn ghost icon-only" aria-label="The day before" onClick={() => go(addDays(day, -1))}><Icon name="chevron-left" size={16} /></button>
        <b className="day-label">{day === todayISO() ? 'Today' : day === addDays(todayISO(), -1) ? 'Yesterday' : `${weekday(day, true)}, ${fmtDate(day)}`}</b>
        <button type="button" className="btn ghost icon-only" aria-label="The day after" disabled={day >= todayISO()} onClick={() => go(addDays(day, 1))}><Icon name="chevron-right" size={16} /></button>
        <input type="date" value={day} max={todayISO()} onChange={e => e.target.value && go(e.target.value)} aria-label="Day" style={{ maxWidth: 160 }} />
        <span className="grow" />
        {!!tl?.modes.length && <span className="muted small ellipsis">{tl.modes.map(m => `${fmtTime(new Date(m.at))} ${MODE[m.mode]?.label}`).join(' · ')}</span>}
      </div>
      {row && <div className="card timeline-player">
        <div className="card-head"><h3>{row.camera.name}</h3>{at && <span className="muted small tnum">{fmtTime(new Date(at))}</span>}<span className="grow" /><button type="button" className="btn ghost icon-only sm" aria-label="Close" onClick={() => setSel(null)}><Icon name="x" size={15} /></button></div>
        <Player row={row} start={sel!.t} onTime={setAt} />
      </div>}
      {!tl ? <Loading /> : !tl.rows.length ? <Empty title="No cameras yet" icon="camera"><p>Add a camera and its day shows here.</p></Empty> : (
        <div className="tl-rows card">
          {tl.rows.map(r => (
            <div key={r.camera.id} className={`tl-row ${sel?.cam === r.camera.id ? 'on' : ''}`}>
              <Link to={`/cameras/${r.camera.id}`} className="tl-name ellipsis" title={r.camera.name}>{r.camera.name}<span className="muted small">{r.events.filter(e => !e.nothing).length || ''}</span></Link>
              <DayBar day={day} row={r} modes={tl.modes} at={sel?.cam === r.camera.id ? at : null} compact
                onPick={t => pick(r.camera.id, r.camera.kind, r.pieces.length > 0, t)}
                onEvent={e => pick(r.camera.id, r.camera.kind, r.pieces.length > 0, Date.parse(e.started_at) - 3000, e)} />
            </div>
          ))}
          <div className="tl-row tl-hours"><span /><div className="daybar-hours">{[0, 3, 6, 9, 12, 15, 18, 21].map(h => <span key={h} style={{ left: `${(h / 24) * 100}%` }}>{String(h).padStart(2, '0')}</span>)}</div></div>
        </div>
      )}
      <div className="legend small muted">
        <span><i className="lg lg-rec" />Recorded</span><span><i className="lg tone-person" />Person</span><span><i className="lg tone-vehicle" />Vehicle</span><span><i className="lg tone-parcel" />Parcel</span><span><i className="lg tone-animal" />Animal</span><span><i className="lg tone-worry" />Worth a look</span><span><i className="lg mode-away" />Away</span><span><i className="lg mode-night" />Night</span>
      </div>
    </div>
  )
}
