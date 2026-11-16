// Live: the mode, every camera's picture now, and the last things seen.
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { announceChange, useOnChange } from '../lib/data'
import { LABELS, MODE, pictureURL } from '../lib/cams'
import type { CamEvent, Camera, Home, Mode, ModeInfo } from '../lib/types'
import { ago, fmtTime } from '../lib/format'
import { Empty, Loading, useToast, errMsg } from '../components/ui'
import { Icon } from '../components/icons'
import { Dot, EventIcon, LabelChips, LivePicture, ModeSwitch } from '../components/bits'
import { openCreate } from '../components/Layout'
import { DiscoverDialog, CameraDialog } from '../components/dialogs'

export default function Live() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { toast } = useToast()
  const [cams, setCams] = useState<Camera[] | null>(null)
  const [mode, setMode] = useState<ModeInfo | null>(null)
  const [home, setHome] = useState<Home | null>(null)
  const [recent, setRecent] = useState<CamEvent[]>([])
  const [busy, setBusy] = useState(false)
  const [find, setFind] = useState(false)
  const [prefill, setPrefill] = useState<{ host: string; brand: string; name: string } | null>(null)
  const load = useCallback(() => {
    api.get<{ cameras: Camera[] }>(`${V1}/cameras`).then(r => setCams(r.cameras)).catch(() => setCams([]))
    api.get<ModeInfo>(`${V1}/mode`).then(setMode).catch(() => {})
    api.get<Home>(`${V1}/home`).then(setHome).catch(() => {})
    api.get<{ events: CamEvent[] }>(`${V1}/events?limit=8`).then(r => setRecent(r.events)).catch(() => {})
  }, [])
  useEffect(() => { load() }, [load])
  useOnChange(load)
  useEffect(() => {
    const t = window.setInterval(() => { if (document.visibilityState === 'visible') load() }, 20000)
    return () => window.clearInterval(t)
  }, [load])
  useEffect(() => {
    if (params.get('mode')) { document.querySelector('.mode-card')?.scrollIntoView({ block: 'center' }); params.delete('mode'); setParams(params, { replace: true }) }
  }, [params, setParams])

  const change = async (m: Mode) => {
    setBusy(true)
    try { setMode(await api.put<ModeInfo>(`${V1}/mode`, { mode: m })); announceChange(); toast(`${MODE[m].label} mode`) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }

  if (!cams) return <Loading />
  const counts = home?.today || {}
  return (
    <div className="live-page">
      {mode && <section className="mode-card">
        <ModeSwitch value={mode.mode} onChange={change} busy={busy} />
        <div className="mode-line muted small">
          {mode.auto_mode ? <><Icon name="refresh" size={12} className="inline-ico" /> Switches by itself{mode.reason ? ` — now ${MODE[mode.mode].label.toLowerCase()}: ${mode.reason}` : ''}{mode.by === 'you' && mode.auto?.mode && mode.auto.mode !== mode.mode ? `. You set ${MODE[mode.mode].label}; it goes back to switching by itself when that changes.` : '.'}</>
            : <>{mode.at ? `Set ${ago(mode.at)}${mode.by && mode.by !== 'you' ? ` by ${mode.by}` : ''}${mode.reason ? ` — ${mode.reason}` : ''}. ` : ''}<Link to="/settings/general">Switch by itself…</Link></>}
        </div>
        {!!counts.events && <div className="today-chips">
          <span className="muted small">Today</span>
          {LABELS.filter(l => counts[l.id]).map(l => <Link key={l.id} to={`/events?label=${l.id}&day=today`} className={`chip tone-${l.tone}`}><Icon name={l.icon} size={12} />{counts[l.id]} {counts[l.id] === 1 ? l.label.toLowerCase() : l.many.toLowerCase()}</Link>)}
          {!!counts.doorbell && <Link to="/events?label=doorbell&day=today" className="chip tone-doorbell"><Icon name="bell" size={12} />{counts.doorbell} at the door</Link>}
        </div>}
      </section>}

      {!cams.length ? (
        <Empty title="No cameras yet" icon="camera">
          <p>Add your first camera: choose its make and type its address on your network, or let Pocket Cameras look for it.</p>
          <div className="row wrap" style={{ justifyContent: 'center' }}>
            <button type="button" className="btn primary" onClick={() => openCreate()}><Icon name="plus" size={15} />Add a camera</button>
            <button type="button" className="btn" onClick={() => setFind(true)}><Icon name="scan" size={15} />Find cameras</button>
          </div>
        </Empty>
      ) : (
        <div className="cam-grid">
          {cams.map(c => (
            <button key={c.id} type="button" className={`cam-tile ${!c.enabled ? 'is-off' : ''} ${c.state.event ? 'is-moving' : ''}`} onClick={() => navigate(`/cameras/${c.id}`)}>
              {c.enabled ? <LivePicture id={c.id} every={c.state.event ? 700 : 1500} alt={c.name} /> : <div className="live-pic"><div className="live-pic-off"><Icon name="pause" size={22} /><span>Switched off</span></div></div>}
              <div className="cam-tile-bar">
                <Dot tone={!c.enabled ? 'off' : c.state.online ? (c.state.event ? 'move' : 'ok') : 'bad'} title={!c.enabled ? 'Switched off' : c.state.online ? 'Online' : c.state.error || 'Offline'} />
                <span className="grow ellipsis cam-tile-name">{c.name}</span>
                {c.state.recording && <span className="rec-badge" title="Recording"><Icon name="rec" size={9} strokeWidth={0} />REC</span>}
                {!c.mine && <Icon name="users" size={13} title={`Shared by ${c.owner}`} />}
              </div>
              {c.last_event && <div className="cam-tile-last">
                <LabelChips labels={c.last_event.labels} pet={c.last_event.pet} small />
                <span className="ellipsis">{ago(c.last_event.started_at)}</span>
              </div>}
            </button>
          ))}
        </div>
      )}

      {!!recent.length && <section className="card recent-card">
        <div className="card-head"><h3>Last seen</h3><Link to="/events" className="btn ghost sm">All events<Icon name="chevron-right" size={14} /></Link></div>
        <div className="ev-list">
          {recent.map(e => (
            <Link key={e.id} to={`/events/${e.id}`} className={`ev-row ${e.reviewed ? '' : 'unseen'}`}>
              {e.frames > 0 ? <img className="ev-thumb" src={pictureURL(e)} alt="" loading="lazy" /> : <EventIcon e={e} size={40} />}
              <div className="grow ev-text">
                <div className="ev-title ellipsis">{e.summary || e.title}</div>
                <div className="muted small ellipsis">{e.camera} · {fmtTime(new Date(e.started_at))} · {ago(e.started_at)}</div>
              </div>
              <LabelChips labels={e.labels} pet={e.pet} small />
            </Link>
          ))}
        </div>
      </section>}
      {find && <DiscoverDialog onClose={() => setFind(false)} onPick={f => { setFind(false); setPrefill({ host: f.ip, brand: f.brand, name: f.name }) }} />}
      {prefill && <CameraDialog prefill={{ host: prefill.host, brand: prefill.brand, name: '' }} onClose={() => setPrefill(null)} onSaved={c => navigate(`/cameras/${c.id}`)} />}
    </div>
  )
}
