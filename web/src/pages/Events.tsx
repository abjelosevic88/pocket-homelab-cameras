// Events: everything the cameras saw, newest first; and one event up close.
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { announceChange, useOnChange } from '../lib/data'
import { LABELS, MODE, eventLength, mainLabel, pictureURL } from '../lib/cams'
import type { CamEvent, Camera } from '../lib/types'
import { addDays, ago, fmtDate, fmtTime, todayISO, weekday } from '../lib/format'
import { Confirm, Empty, Loading, Segmented, useDebounce, useToast, errMsg } from '../components/ui'
import { Icon } from '../components/icons'
import { AppButton, EventIcon, InfoGrid, LabelChips } from '../components/bits'
import { Select } from '../components/Select'
import { useCrumb } from '../components/Layout'

const WHEN = [{ value: 'today', label: 'Today' }, { value: 'yesterday', label: 'Yesterday' }, { value: '7', label: '7 days' }, { value: '30', label: '30 days' }, { value: 'all', label: 'All' }]

export default function Events() {
  const [params, setParams] = useSearchParams()
  const { toast } = useToast()
  const [list, setList] = useState<CamEvent[] | null>(null)
  const [total, setTotal] = useState(0)
  const [cams, setCams] = useState<Camera[]>([])
  const [q, setQ] = useState(params.get('q') || '')
  const dq = useDebounce(q, 300)
  const camera = params.get('camera') || ''
  const label = params.get('label') || ''
  const when = params.get('day') || params.get('when') || '7'
  const unseen = params.get('unreviewed') === '1'
  const starred = params.get('starred') === '1'
  const nothing = params.get('nothing') === '1'
  const [limit, setLimit] = useState(100)
  const set = (k: string, v: string) => { if (v) params.set(k, v); else params.delete(k); if (k === 'when') params.delete('day'); setParams(params, { replace: true }); setLimit(100) }
  const query = useMemo(() => {
    const p = new URLSearchParams({ limit: String(limit) })
    if (camera) p.set('camera', camera)
    if (label) p.set('label', label)
    if (when === 'today') p.set('day', todayISO())
    else if (when === 'yesterday') p.set('day', addDays(todayISO(), -1))
    else if (when !== 'all') p.set('from', addDays(todayISO(), -Number(when) + 1))
    if (unseen) p.set('unreviewed', '1')
    if (starred) p.set('starred', '1')
    if (nothing) p.set('nothing', '1')
    if (dq) p.set('q', dq)
    return p.toString()
  }, [camera, label, when, unseen, starred, nothing, dq, limit])
  const load = useCallback(() => {
    api.get<{ events: CamEvent[]; total: number }>(`${V1}/events?${query}`).then(r => { setList(r.events); setTotal(r.total) }).catch(() => setList([]))
  }, [query])
  useEffect(() => { load() }, [load])
  useOnChange(load)
  useEffect(() => { api.get<{ cameras: Camera[] }>(`${V1}/cameras`).then(r => setCams(r.cameras)).catch(() => {}) }, [])
  const reviewAll = async () => {
    try { const r = await api.post<{ reviewed: number }>(`${V1}/events/review`, { camera_id: Number(camera) || 0 }); toast(`${r.reviewed} marked as seen`); announceChange(); load() } catch (e) { toast(errMsg(e), 'err') }
  }
  const groups = useMemo(() => {
    const out: { day: string; items: CamEvent[] }[] = []
    for (const e of list || []) {
      const d = new Date(e.started_at)
      const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      if (!out.length || out[out.length - 1].day !== day) out.push({ day, items: [] })
      out[out.length - 1].items.push(e)
    }
    return out
  }, [list])
  const dayName = (d: string) => d === todayISO() ? 'Today' : d === addDays(todayISO(), -1) ? 'Yesterday' : `${weekday(d, true)}, ${fmtDate(d)}`
  return (
    <div className="events-page stack">
      <div className="filters">
        <div className="filters-row">
          <Segmented size="sm" value={when} onChange={v => set('when', v)} options={WHEN} />
          <Select ariaLabel="Camera" value={camera} onChange={v => set('camera', v)} placeholder="Every camera" clearLabel="Every camera" options={cams.map(c => ({ value: String(c.id), label: c.name }))} />
          <div className="search-box"><Icon name="search" size={14} /><input value={q} onChange={e => { setQ(e.target.value); set('q', e.target.value) }} placeholder="Search: “courier”, “fox”…" aria-label="Search events" /></div>
        </div>
        <div className="filters-row chips">
          <button type="button" className={`chip-toggle ${!label ? 'on' : ''}`} onClick={() => set('label', '')}>Everything</button>
          {LABELS.map(l => <button key={l.id} type="button" className={`chip-toggle ${label === l.id ? 'on' : ''}`} onClick={() => set('label', label === l.id ? '' : l.id)}><Icon name={l.icon} size={13} />{l.many}</button>)}
          <button type="button" className={`chip-toggle ${label === 'doorbell' ? 'on' : ''}`} onClick={() => set('label', label === 'doorbell' ? '' : 'doorbell')}><Icon name="bell" size={13} />Doorbell</button>
          <span className="chip-sep" />
          <button type="button" className={`chip-toggle ${unseen ? 'on' : ''}`} onClick={() => set('unreviewed', unseen ? '' : '1')}><Icon name="eye" size={13} />Not seen</button>
          <button type="button" className={`chip-toggle ${starred ? 'on' : ''}`} onClick={() => set('starred', starred ? '' : '1')}><Icon name="star" size={13} />Kept</button>
          <button type="button" className={`chip-toggle ${nothing ? 'on' : ''}`} onClick={() => set('nothing', nothing ? '' : '1')} title="Show false alarms too">False alarms</button>
        </div>
      </div>
      <div className="row">
        <span className="muted small">{list ? `${total.toLocaleString()} ${total === 1 ? 'event' : 'events'}` : ''}</span>
        <span className="grow" />
        <button type="button" className="btn ghost sm" onClick={reviewAll}><Icon name="check" size={14} />Mark all as seen</button>
      </div>
      {!list ? <Loading /> : !list.length ? <Empty title="Nothing here" icon="bell"><p>No events with these filters.</p></Empty> : <>
        {groups.map(g => (
          <section key={g.day}>
            <h3 className="day-h">{dayName(g.day)}<span className="muted small">{g.items.length}</span></h3>
            <div className="ev-grid">
              {g.items.map(e => <EventCard key={e.id} e={e} />)}
            </div>
          </section>
        ))}
        {list.length < total && <button type="button" className="btn" style={{ justifySelf: 'center' }} onClick={() => setLimit(l => l + 100)}>Show more</button>}
      </>}
    </div>
  )
}

function EventCard({ e }: { e: CamEvent }) {
  const m = mainLabel(e)
  return (
    <Link to={`/events/${e.id}`} className={`ev-tile ${e.reviewed ? '' : 'unseen'} ${e.nothing ? 'is-nothing' : ''}`}>
      <div className="ev-tile-pic">
        {e.frames > 0 ? <img src={pictureURL(e)} alt="" loading="lazy" /> : <div className="ev-tile-ico"><EventIcon e={e} size={44} /></div>}
        <span className="ev-tile-time">{fmtTime(new Date(e.started_at))}</span>
        {e.starred && <span className="ev-tile-star"><Icon name="star" size={13} /></span>}
        {e.worry > 0 && !e.nothing && <span className="ev-tile-worry" title={e.worry > 1 ? 'Urgent' : 'Worth a look'}><Icon name="alert" size={13} /></span>}
      </div>
      <div className="ev-tile-body">
        <div className="row tight"><span className={`ev-kind tone-${e.nothing ? 'off' : m.tone}`}><Icon name={m.icon} size={12} />{e.nothing ? 'Nothing' : e.pet && m.label === 'Animal' ? e.pet : m.label}</span><span className="muted small ellipsis">{e.camera}</span></div>
        <div className="ev-tile-text">{e.summary || (e.ai_state === 'pending' ? 'Looking…' : 'Movement')}</div>
      </div>
    </Link>
  )
}

export function EventPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const [e, setE] = useState<CamEvent | null>(null)
  const [err, setErr] = useState('')
  const [frame, setFrame] = useState(0)
  const [clip, setClip] = useState(false)
  const [note, setNote] = useState('')
  const [del, setDel] = useState(false)
  const [next, setNext] = useState<number | null>(null)
  const load = useCallback(() => {
    api.get<CamEvent>(`${V1}/events/${id}`).then(x => { setE(x); setNote(x.note); setFrame(x.best); setErr('') }).catch(er => setErr(errMsg(er)))
  }, [id])
  useEffect(() => { setClip(false); load() }, [load])
  useCrumb(e ? `${e.camera}, ${fmtTime(new Date(e.started_at))}` : undefined, '/events')
  // looking at it marks it seen
  useEffect(() => {
    if (e && !e.reviewed && e.mine) api.put(`${V1}/events/${e.id}`, { reviewed: true }).then(() => announceChange()).catch(() => {})
  }, [e?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  // the event after this one (same camera)
  useEffect(() => {
    if (!e) return
    api.get<{ events: CamEvent[] }>(`${V1}/events?camera=${e.camera_id}&from=${e.started_at.slice(0, 10)}&limit=200&nothing=1`).then(r => {
      const later = r.events.filter(x => x.started_at > e.started_at)
      setNext(later.length ? later[later.length - 1].id : null)
    }).catch(() => {})
  }, [e])
  const patch = async (p: Record<string, unknown>, msg?: string) => {
    if (!e) return
    try { const x = await api.put<CamEvent>(`${V1}/events/${e.id}`, p); setE({ ...e, ...x }); if (msg) toast(msg); announceChange() } catch (er) { toast(errMsg(er), 'err') }
  }
  useEffect(() => {
    const h = (ev: KeyboardEvent) => {
      if ((ev.target as HTMLElement).closest('input, textarea') || document.querySelector('.modal-backdrop, .menu')) return
      if (ev.key === 'ArrowLeft' && e?.prev_id) navigate(`/events/${e.prev_id}`)
      if (ev.key === 'ArrowRight' && next) navigate(`/events/${next}`)
      if (ev.key === 'k' && e?.mine) patch({ starred: !e.starred }, e.starred ? 'No longer kept' : 'Kept')
      if (ev.key === 'x' && e?.mine) patch({ nothing: !e.nothing }, e.nothing ? 'Marked as something' : 'Marked as nothing there')
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  })
  if (err) return <Empty title="Event not found" icon="bell"><p>{err}</p><Link to="/events" className="btn">All events</Link></Empty>
  if (!e) return <Loading />
  const t = new Date(e.started_at)
  const canClip = e.clip || e.recorded || e.frames >= 2
  const box = e.box ? e.box.split(',').map(Number) : null
  return (
    <div className="event-page">
      <div className="event-view">
        <div className="event-media">
          {clip ? <video src={`${V1}/events/${e.id}/clip.mp4`} controls autoPlay muted playsInline className="event-video" onError={() => { toast('The clip could not be made', 'err'); setClip(false) }} />
            : e.frames > 0 ? <div className="event-pic">
              <img src={pictureURL(e, frame)} alt={e.summary} />
              {box && frame === e.best && !e.nothing && <span className="event-box" style={{ left: `${box[0] * 100}%`, top: `${box[1] * 100}%`, width: `${box[2] * 100}%`, height: `${box[3] * 100}%` }} />}
            </div> : <div className="event-pic empty"><EventIcon e={e} size={64} /></div>}
          <div className="event-media-bar">
            <button type="button" className="btn ghost icon-only" disabled={!e.prev_id} onClick={() => navigate(`/events/${e.prev_id}`)} aria-label="The event before" title="The event before (←)"><Icon name="chevron-left" size={16} /></button>
            {e.frames > 1 && !clip && <input type="range" min={1} max={e.frames} value={frame} onChange={x => setFrame(Number(x.target.value))} aria-label="Picture" className="grow" />}
            {(clip || e.frames <= 1) && <span className="grow" />}
            {canClip && <button type="button" className={`btn sm ${clip ? '' : 'primary'}`} onClick={() => setClip(c => !c)}><Icon name={clip ? 'image' : 'play'} size={14} />{clip ? 'Pictures' : 'Play the clip'}</button>}
            <button type="button" className="btn ghost icon-only" disabled={!next} onClick={() => navigate(`/events/${next}`)} aria-label="The event after" title="The event after (→)"><Icon name="chevron-right" size={16} /></button>
          </div>
        </div>
        <div className="event-side">
          <div className="event-head">
            <EventIcon e={e} size={40} />
            <div className="grow">
              <h2>{e.title}</h2>
              <div className="muted small">{weekday(e.started_at.slice(0, 10), true)} {fmtDate(e.started_at.slice(0, 10))}, {fmtTime(t)} · {ago(e.started_at)}</div>
            </div>
          </div>
          <p className="event-summary">{e.nothing ? (e.summary || 'Nothing there.') : e.summary || (e.ai_state === 'pending' ? 'The AI is looking at it…' : e.ai_state === 'failed' ? 'The AI could not look at it.' : 'Something moved.')}</p>
          <LabelChips labels={e.labels} pet={e.pet} />
          {e.parcel_text && <div className="callout info"><Icon name="package" size={15} /><div>Probably your parcel: <b>{e.parcel_text}</b>{e.parcel_url && <> · <a href={e.parcel_url} target="_blank" rel="noreferrer">Open in Pocket Parcels</a></>}</div></div>}
          {e.mine && <div className="row wrap">
            <button type="button" className={`btn sm ${e.starred ? 'primary' : ''}`} onClick={() => patch({ starred: !e.starred }, e.starred ? 'No longer kept' : 'Kept — it stays, with its clip')} title="Keep it (k)"><Icon name="star" size={14} />{e.starred ? 'Kept' : 'Keep'}</button>
            <button type="button" className={`btn sm ${e.nothing ? 'primary' : ''}`} onClick={() => patch({ nothing: !e.nothing })} title="A false alarm (x)"><Icon name="x-circle" size={14} />Nothing there</button>
            <a className="btn sm ghost" href={`${pictureURL(e, frame)}${frame ? '&' : '?'}download=1`}><Icon name="download" size={14} />Picture</a>
            {canClip && <a className="btn sm ghost" href={`${V1}/events/${e.id}/clip.mp4?download=1`}><Icon name="download" size={14} />Clip</a>}
          </div>}
          <InfoGrid items={[
            ['Camera', <Link key="c" to={`/cameras/${e.camera_id}`}>{e.camera}</Link>],
            ['Length', eventLength(e)],
            ['Mode', e.mode ? MODE[e.mode]?.label : ''],
            ['Alert', e.alerted ? 'Sent' : e.nothing ? '' : 'None'],
            ['Weather', e.weather],
            ['From', e.source && e.source !== 'webhook' ? e.source : e.source === 'webhook' ? 'a button (webhook)' : ''],
          ]} />
          {e.mine && <div className="stack tight">
            <label className="small muted" htmlFor="ev-note">Note</label>
            <textarea id="ev-note" rows={2} value={note} onChange={x => setNote(x.target.value)} onBlur={() => note !== e.note && patch({ note })} placeholder="What it was, what you did…" />
          </div>}
          <div className="send-to">
            <h4 className="small muted">Send to</h4>
            <div className="row wrap">
              <AppButton slug="chat" scope="chat:write" label="Chat" icon="message" path={`${V1}/events/${e.id}/chat`} body={{ clip: false }} />
              <AppButton slug="tasks" scope="tasks:write" label="Task" icon="check-square" path={`${V1}/events/${e.id}/task`} />
              <AppButton slug="notes" scope="notes:write" label="Notes" icon="note" path={`${V1}/events/${e.id}/note`} />
              <AppButton slug="photos" scope="photos:write" label="Photos" icon="image" path={`${V1}/events/${e.id}/photos`} confirm={() => ({ frame })} />
              <AppButton slug="files" scope="files:write" label="Files" icon="archive" path={`${V1}/events/${e.id}/files`} />
            </div>
          </div>
          {e.mine && <button type="button" className="btn ghost sm danger-text" style={{ justifySelf: 'start' }} onClick={() => setDel(true)}><Icon name="trash" size={14} />Remove</button>}
        </div>
      </div>
      {del && <Confirm title="Remove this event?" message="Its pictures and clip go too." confirmLabel="Remove" onCancel={() => setDel(false)} onConfirm={async () => { setDel(false); await api.del(`${V1}/events/${e.id}`).catch(() => {}); announceChange(); navigate('/events') }} />}
    </div>
  )
}

