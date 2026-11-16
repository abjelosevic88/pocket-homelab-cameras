// One camera's day: a 24-hour bar with what was recorded and the events, and the video.
import { useEffect, useMemo, useRef, useState } from 'react'
import { V1 } from '../lib/api'
import { mainLabel } from '../lib/cams'
import type { CamEvent, ModeChange, TimelineRow } from '../lib/types'
import { fmtTime } from '../lib/format'
import { Icon } from './icons'

const DAY = 86400000

export function dayStart(day: string): number {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(y, m - 1, d).getTime()
}

/** The 24-hour bar: recorded stretches, events, the mode, and where you are. */
export function DayBar({ day, row, modes, at, onPick, onEvent, compact }: { day: string; row: TimelineRow; modes?: ModeChange[]; at?: number | null; onPick: (t: number) => void; onEvent?: (e: CamEvent) => void; compact?: boolean }) {
  const t0 = dayStart(day)
  const pct = (t: number) => `${Math.min(100, Math.max(0, ((t - t0) / DAY) * 100))}%`
  const width = (a: number, b: number) => `${Math.max(0.15, ((Math.min(b, t0 + DAY) - Math.max(a, t0)) / DAY) * 100)}%`
  const bar = useRef<HTMLDivElement>(null)
  const click = (e: React.MouseEvent) => {
    const r = bar.current!.getBoundingClientRect()
    onPick(t0 + ((e.clientX - r.left) / r.width) * DAY)
  }
  const now = Date.now()
  return (
    <div className={`daybar ${compact ? 'compact' : ''}`}>
      <div className="daybar-track" ref={bar} onClick={click} title="Play from here">
        {modes?.filter(m => m.mode !== 'home').map(m => {
          const a = Date.parse(m.at)
          const next = modes.find(x => Date.parse(x.at) > a)
          const b = next ? Date.parse(next.at) : Math.min(now, t0 + DAY)
          return <span key={m.id} className={`daybar-mode mode-${m.mode}`} style={{ left: pct(a), width: width(a, b) }} />
        })}
        {row.spans.map(([a, b], i) => <span key={i} className="daybar-rec" style={{ left: pct(Date.parse(a)), width: width(Date.parse(a), Date.parse(b)) }} />)}
        {row.events.map(e => (
          <button key={e.id} type="button" className={`daybar-ev tone-${e.nothing ? 'off' : e.worry ? 'worry' : mainLabel(e).tone}`} style={{ left: pct(Date.parse(e.started_at)) }}
            title={`${fmtTime(new Date(e.started_at))} · ${e.summary || e.title}`} onClick={ev => { ev.stopPropagation(); onEvent ? onEvent(e) : onPick(Date.parse(e.started_at) - 3000) }} />
        ))}
        {at != null && at >= t0 && at < t0 + DAY && <span className="daybar-head" style={{ left: pct(at) }} />}
        {now >= t0 && now < t0 + DAY && <span className="daybar-now" style={{ left: pct(now) }} />}
      </div>
      {!compact && <div className="daybar-hours">{[0, 3, 6, 9, 12, 15, 18, 21].map(h => <span key={h} style={{ left: `${(h / 24) * 100}%` }}>{String(h).padStart(2, '0')}</span>)}</div>}
    </div>
  )
}

/** Plays a camera's recordings from a moment, piece after piece. */
export function Player({ row, start, onTime }: { row: TimelineRow; start: number | null; onTime?: (t: number) => void }) {
  const video = useRef<HTMLVideoElement>(null)
  const [piece, setPiece] = useState<number>(-1)
  const [offset, setOffset] = useState(0)
  const pieces = useMemo(() => row.pieces.map(p => ({ id: p.id, a: Date.parse(p.start), b: Date.parse(p.end) })), [row.pieces])
  useEffect(() => {
    if (start == null || !pieces.length) { setPiece(-1); return }
    let i = pieces.findIndex(p => p.a <= start && start < p.b)
    if (i < 0) i = pieces.findIndex(p => p.a > start)
    if (i < 0) { setPiece(-1); return }
    setPiece(i)
    setOffset(Math.max(0, (start - pieces[i].a) / 1000))
  }, [start, pieces])
  const p = piece >= 0 ? pieces[piece] : null
  if (!row.pieces.length) {
    return <div className="player-empty"><Icon name="video" size={22} /><span>{row.camera.kind === 'rtsp' ? (row.camera.record === 'off' ? 'This camera does not record.' : 'Nothing was recorded this day.') : 'This camera keeps pictures of its events, not video.'}</span></div>
  }
  if (!p) return <div className="player-empty"><Icon name="play" size={22} /><span>Tap the bar or an event to play from there.</span></div>
  return (
    <video ref={video} className="player" key={p.id} src={`${V1}/segments/${p.id}/video`} controls autoPlay muted playsInline
      onLoadedMetadata={e => { (e.target as HTMLVideoElement).currentTime = offset; setOffset(0) }}
      onTimeUpdate={e => onTime?.(p.a + (e.target as HTMLVideoElement).currentTime * 1000)}
      onEnded={() => { const n = piece + 1; if (n < pieces.length && pieces[n].a - p.b < 5000) setPiece(n) }} />
  )
}
