// One camera: live, its day (recordings and events), the areas it watches, and how it behaves.
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange, useAppState, useOnChange } from '../lib/data'
import { KIND_WORDS, LABELS, MODES, pictureURL, snapshotURL } from '../lib/cams'
import type { CamEvent, Camera, Label, Mode, Share, Timeline, Zone } from '../lib/types'
import { addDays, ago, fmtBytes, fmtDate, fmtTime, todayISO } from '../lib/format'
import { Confirm, CopyField, Empty, Loading, SettingRow, Switch, Segmented, Tabs, useToast, errMsg, CoreHint } from '../components/ui'
import { Icon } from '../components/icons'
import { AppHint, Dot, EventIcon, InfoGrid, LabelChips, LiveStream } from '../components/bits'
import { DayBar, Player } from '../components/Player'
import { ZonesEditor } from '../components/Zones'
import { CameraDialog, useList } from '../components/dialogs'
import { Select } from '../components/Select'
import { useCrumb } from '../components/Layout'

type Tab = 'day' | 'events' | 'areas' | 'settings'

export default function CameraPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const [cam, setCam] = useState<Camera | null>(null)
  const [err, setErr] = useState('')
  const [hd, setHd] = useState(false)
  const tab = (params.get('tab') as Tab) || 'day'
  const load = useCallback(() => { api.get<Camera>(`${V1}/cameras/${id}`).then(c => { setCam(c); setErr('') }).catch(e => setErr(errMsg(e))) }, [id])
  useEffect(() => { load() }, [load])
  // the camera's state (online, moving, recording) changes on its own: look again every few seconds
  useEffect(() => {
    const t = window.setInterval(() => { if (document.visibilityState === 'visible') load() }, 5000)
    return () => window.clearInterval(t)
  }, [load])
  useOnChange(load)
  useCrumb(cam?.name, '/cameras')
  if (err) return <Empty title="Camera not found" icon="camera"><p>{err}</p><Link to="/" className="btn">Back to live</Link></Empty>
  if (!cam) return <Loading />
  const tabs: { id: Tab; label: string; icon: string }[] = [{ id: 'day', label: 'Day', icon: 'video' }, { id: 'events', label: 'Events', icon: 'bell' }]
  if (cam.mine) tabs.push({ id: 'areas', label: 'Areas', icon: 'target' }, { id: 'settings', label: 'Settings', icon: 'settings' })
  const st = cam.state
  return (
    <div className="cam-page">
      <div className="cam-live">
        {cam.enabled ? <LiveStream id={cam.id} hd={hd} smooth={cam.kind === 'rtsp'} alt={cam.name} /> : <div className="live-pic"><div className="live-pic-off"><Icon name="pause" size={26} /><span>Switched off — switch it on in Settings below</span></div></div>}
        <div className="cam-live-bar">
          <Dot tone={!cam.enabled ? 'off' : st.online ? (st.event ? 'move' : 'ok') : 'bad'} />
          <span className="small">{!cam.enabled ? 'Switched off' : st.online ? (st.event ? 'Something is moving' : 'Live') : `Offline${st.error ? ` — ${st.error}` : ''}`}</span>
          {st.recording && <span className="rec-badge"><Icon name="rec" size={9} strokeWidth={0} />REC</span>}
          <span className="grow" />
          {cam.kind === 'rtsp' && cam.enabled && <button type="button" className={`btn sm ${hd ? 'primary' : 'ghost'}`} onClick={() => setHd(h => !h)} title="Full quality (uses more of the server and your connection)">HD</button>}
          <a className="btn ghost sm icon-only" href={`${snapshotURL(cam.id)}?download=1`} title="Save a picture" aria-label="Save a picture"><Icon name="download" size={15} /></a>
        </div>
      </div>
      {!cam.mine && <p className="muted small"><Icon name="users" size={12} className="inline-ico" /> {cam.owner} shares this camera with you: you can watch it and see what it saw.</p>}
      {st.rec_error && cam.record !== 'off' && <div className="callout warn"><Icon name="alert" size={15} /><div>Recording: {st.rec_error}</div></div>}
      <Tabs tabs={tabs} value={tab} onChange={t => { params.set('tab', t); setParams(params, { replace: true }) }} />
      {tab === 'day' && <DayTab cam={cam} />}
      {tab === 'events' && <CamEvents cam={cam} />}
      {tab === 'areas' && cam.mine && <AreasTab cam={cam} onSaved={load} />}
      {tab === 'settings' && cam.mine && <SettingsTab cam={cam} onSaved={load} onDeleted={() => navigate('/cameras')} />}
      {!cam.mine && <ViewerAlerts cam={cam} onSaved={load} />}
    </div>
  )
}

function DayTab({ cam }: { cam: Camera }) {
  const [day, setDay] = useState(todayISO())
  const [tl, setTl] = useState<Timeline | null>(null)
  const [start, setStart] = useState<number | null>(null)
  const [at, setAt] = useState<number | null>(null)
  const navigate = useNavigate()
  useEffect(() => { setTl(null); api.get<Timeline>(`${V1}/timeline?day=${day}&camera=${cam.id}`).then(setTl).catch(() => {}) }, [day, cam.id])
  const row = tl?.rows[0]
  return (
    <div className="stack">
      <div className="row day-nav">
        <button type="button" className="btn ghost icon-only" aria-label="The day before" onClick={() => { setDay(addDays(day, -1)); setStart(null) }}><Icon name="chevron-left" size={16} /></button>
        <b className="day-label">{day === todayISO() ? 'Today' : day === addDays(todayISO(), -1) ? 'Yesterday' : fmtDate(day)}</b>
        <button type="button" className="btn ghost icon-only" aria-label="The day after" disabled={day >= todayISO()} onClick={() => { setDay(addDays(day, 1)); setStart(null) }}><Icon name="chevron-right" size={16} /></button>
        {day !== todayISO() && <button type="button" className="btn ghost sm" onClick={() => { setDay(todayISO()); setStart(null) }}>Today</button>}
        <span className="grow" />
        {row && <span className="muted small">{row.events.length} {row.events.length === 1 ? 'event' : 'events'}</span>}
      </div>
      {!tl ? <Loading /> : row && <>
        <DayBar day={day} row={row} modes={tl.modes} at={at} onPick={t => setStart(t)} onEvent={e => cam.kind === 'rtsp' && row.pieces.length ? setStart(Date.parse(e.started_at) - 3000) : navigate(`/events/${e.id}`)} />
        {cam.kind === 'rtsp' && <Player row={row} start={start} onTime={setAt} />}
        {!!row.events.length && <div className="ev-strip">
          {row.events.map(e => (
            <Link key={e.id} to={`/events/${e.id}`} className="ev-card">
              {e.frames > 0 ? <img src={pictureURL(e)} alt="" loading="lazy" /> : <div className="ev-card-ico"><EventIcon e={e} /></div>}
              <span className="ev-card-time">{fmtTime(new Date(e.started_at))}</span>
              <LabelChips labels={e.labels} pet={e.pet} small />
            </Link>
          ))}
        </div>}
      </>}
    </div>
  )
}

function CamEvents({ cam }: { cam: Camera }) {
  const [list, setList] = useState<CamEvent[] | null>(null)
  useEffect(() => { api.get<{ events: CamEvent[] }>(`${V1}/events?camera=${cam.id}&limit=60`).then(r => setList(r.events)).catch(() => setList([])) }, [cam.id])
  if (!list) return <Loading />
  if (!list.length) return <Empty title="Nothing seen yet" icon="bell"><p>Events appear here when something moves in front of {cam.name}.</p></Empty>
  return (
    <div className="stack">
      <div className="ev-list card">
        {list.map(e => (
          <Link key={e.id} to={`/events/${e.id}`} className={`ev-row ${e.reviewed ? '' : 'unseen'}`}>
            {e.frames > 0 ? <img className="ev-thumb" src={pictureURL(e)} alt="" loading="lazy" /> : <EventIcon e={e} size={40} />}
            <div className="grow ev-text"><div className="ev-title ellipsis">{e.summary || e.title}</div><div className="muted small">{fmtDate(e.started_at.slice(0, 10))} · {fmtTime(new Date(e.started_at))}</div></div>
            <LabelChips labels={e.labels} pet={e.pet} small />
          </Link>
        ))}
      </div>
      <Link to={`/events?camera=${cam.id}`} className="btn ghost sm" style={{ justifySelf: 'start' }}>All of {cam.name}'s events<Icon name="chevron-right" size={14} /></Link>
    </div>
  )
}

function AreasTab({ cam, onSaved }: { cam: Camera; onSaved: () => void }) {
  const { toast } = useToast()
  const [zones, setZones] = useState<Zone[]>(cam.zones)
  const [busy, setBusy] = useState(false)
  const dirty = JSON.stringify(zones) !== JSON.stringify(cam.zones)
  const save = async () => {
    setBusy(true)
    try { await api.put(`${V1}/cameras/${cam.id}`, { zones }); toast('Areas saved'); onSaved() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack">
      <p className="muted">Movement only counts in the areas you watch, and never in the areas you ignore. Watch the path to the door; ignore the street, a tree in the wind, the neighbour's window.</p>
      <ZonesEditor cameraId={cam.id} zones={zones} onChange={setZones} />
      <div className="row"><button type="button" className="btn primary" disabled={!dirty || busy} onClick={save}>Save the areas</button>{dirty && <button type="button" className="btn ghost" onClick={() => setZones(cam.zones)}>Undo changes</button>}</div>
    </div>
  )
}

const SENS = [{ value: 2, label: 'Big things' }, { value: 5, label: 'Normal' }, { value: 8, label: 'Small things' }]

function SettingsTab({ cam, onSaved, onDeleted }: { cam: Camera; onSaved: () => void; onDeleted: () => void }) {
  const { toast } = useToast()
  const { users, user, settings } = useApp()
  const appState = useAppState()
  const home = appState('home', 'home:read')
  const homeOk = home.ok
  const things = useList<{ id: string; name: string; room: string; brand: string; url: string }>('/apps/home/things', 'things', homeOk)
  const [edit, setEdit] = useState(false)
  const [del, setDel] = useState(false)
  const [busy, setBusy] = useState(false)
  const save = async (patch: Record<string, unknown>, msg = 'Saved') => {
    setBusy(true)
    try { await api.put(`${V1}/cameras/${cam.id}`, patch); toast(msg); onSaved(); announceChange() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const toggle = <T extends string>(list: T[], v: T) => list.includes(v) ? list.filter(x => x !== v) : [...list, v]
  const shareKey = async (on: boolean) => {
    try { await api.post(`${V1}/cameras/${cam.id}/share-key`, { on }); onSaved() } catch (e) { toast(errMsg(e), 'err') }
  }
  const shares = cam.shares || []
  const setShares = (s: Share[]) => api.put(`${V1}/cameras/${cam.id}/shares`, { shares: s.map(x => ({ user_id: x.user_id, notify: x.notify })) }).then(onSaved).catch(e => toast(errMsg(e), 'err'))
  const others = users.filter(u => u.id !== user?.id)
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <section className="set-section card-lite">
        <div className="set-section-head"><span className="set-app-ico"><Icon name="cctv" size={17} /></span><div className="grow"><h3>{cam.name}</h3><p>{[KIND_WORDS[cam.kind], cam.brand, cam.host, cam.room].filter(Boolean).join(' · ')}</p></div>
          <button type="button" className="btn sm" onClick={() => setEdit(true)}><Icon name="edit" size={14} />Address, name, room</button></div>
        <SettingRow title="Switched on" help="Off: no live picture, no events, no recording."><Switch checked={cam.enabled} disabled={busy} onChange={v => save({ enabled: v })} label="On" /></SettingRow>
      </section>

      <section className="set-section card-lite">
        <div className="set-section-head"><span className="set-app-ico"><Icon name="bell" size={17} /></span><div className="grow"><h3>When it tells you</h3><p>An alert goes to your phone when it sees one of these, in one of these modes.</p></div></div>
        <SettingRow title="What" stack>
          <div className="chips">{LABELS.map(l => <button key={l.id} type="button" className={`chip-toggle ${cam.labels.includes(l.id) ? 'on' : ''}`} onClick={() => save({ labels: toggle<Label>(cam.labels, l.id) })}><Icon name={l.icon} size={13} />{l.many}</button>)}</div>
        </SettingRow>
        <SettingRow title="In which modes" stack>
          <div className="chips">{MODES.filter(m => m.id !== 'off').map(m => <button key={m.id} type="button" className={`chip-toggle ${cam.modes.includes(m.id) ? 'on' : ''}`} onClick={() => save({ modes: toggle<Mode>(cam.modes, m.id) })}><Icon name={m.icon} size={13} />{m.label}</button>)}</div>
        </SettingRow>
        <SettingRow title="A doorbell" help="A ring always reaches your phone, in every mode (Off too)."><Switch checked={cam.doorbell} onChange={v => save({ doorbell: v })} label="Doorbell" /></SettingRow>
        <SettingRow title="Ask the AI what it is" help="The clearest picture of each event goes to your AI (Settings → AI), which says person, vehicle, animal, parcel — or nothing, which never alerts."><Switch checked={cam.ai_check} onChange={v => save({ ai_check: v })} label="AI" /></SettingRow>
      </section>

      <section className="set-section card-lite">
        <div className="set-section-head"><span className="set-app-ico"><Icon name="activity" size={17} /></span><div className="grow"><h3>Movement</h3></div></div>
        <SettingRow title="Look for movement"><Switch checked={cam.detect} onChange={v => save({ detect: v })} label="Detect" /></SettingRow>
        <SettingRow title="Notice" help="Fewer false alarms with “big things”; a cat across the lawn needs “small things”.">
          <Segmented size="sm" value={SENS.reduce((a, b) => Math.abs(b.value - cam.sensitivity) < Math.abs(a.value - cam.sensitivity) ? b : a).value} onChange={v => save({ sensitivity: v })} options={SENS} />
        </SettingRow>
      </section>

      <section className="set-section card-lite">
        <div className="set-section-head"><span className="set-app-ico"><Icon name="video" size={17} /></span><div className="grow"><h3>Recording</h3><p>{cam.can_record ? `${fmtBytes(cam.bytes)} kept now${cam.recorded_from ? `, since ${fmtDate(cam.recorded_from.slice(0, 10))}` : ''}.` : 'This camera sends pictures, not video: each event keeps its pictures (and a short clip made from them).'}</p></div></div>
        {cam.can_record && <>
          <SettingRow title="Record"><Segmented size="sm" value={cam.record} onChange={v => save({ record: v })} options={[{ value: 'events', label: 'Around events' }, { value: 'always', label: 'All the time' }, { value: 'off', label: 'Never' }]} /></SettingRow>
          <SettingRow title="Keep recordings for"><div className="input-suffix" style={{ maxWidth: 140 }}><input type="number" min={1} max={365} defaultValue={cam.retain_days} onBlur={e => Number(e.target.value) !== cam.retain_days && save({ retain_days: Number(e.target.value) })} /><span>days</span></div></SettingRow>
        </>}
        <SettingRow title="Keep events for" help="Starred events stay until you remove them."><div className="input-suffix" style={{ maxWidth: 140 }}><input type="number" min={1} max={3650} defaultValue={cam.event_days} onBlur={e => Number(e.target.value) !== cam.event_days && save({ event_days: Number(e.target.value) })} /><span>days</span></div></SettingRow>
      </section>

      <section className="set-section card-lite">
        <div className="set-section-head"><span className="set-app-ico"><Icon name="users" size={17} /></span><div className="grow"><h3>Who else can watch</h3><p>People on this box you share it with see it live, its events and recordings, and get its alerts if they want. They cannot change it.</p></div></div>
        {others.length ? others.map(u => {
          const sh = shares.find(x => x.user_id === u.id)
          return <SettingRow key={u.id} title={u.name || u.email} help={sh ? (sh.notify ? 'Watches and gets alerts' : 'Watches, no alerts') : undefined}>
            <div className="row">
              {sh && <Switch size="sm" checked={sh.notify} onChange={v => setShares(shares.map(x => x.user_id === u.id ? { ...x, notify: v } : x))} label="Alerts" />}
              <Switch checked={!!sh} onChange={v => setShares(v ? [...shares, { camera_id: cam.id, user_id: u.id, name: u.name, email: u.email, notify: true }] : shares.filter(x => x.user_id !== u.id))} label="Share" />
            </div>
          </SettingRow>
        }) : <p className="muted small">Nobody else has an account on this box yet (Settings → People).</p>}
      </section>

      <section className="set-section card-lite">
        <div className="set-section-head"><span className="set-app-ico"><Icon name="link" size={17} /></span><div className="grow"><h3>For other things</h3></div></div>
        <SettingRow title="A secret picture address" help="For Home Assistant, a wall tablet or Homepage: anyone with the address sees the camera, without signing in. Switch it off and the address stops working.">
          <Switch checked={!!cam.share_urls} onChange={shareKey} label="Secret address" />
        </SettingRow>
        {cam.share_urls && <div className="stack tight"><CopyField label="Picture" value={cam.share_urls.snapshot} /><CopyField label="Live (MJPEG)" value={cam.share_urls.live} /></div>}
        <SettingRow title="Doorbell and alarm buttons" stack help={<>Send <code>POST</code> with your API token (Settings → API & shortcuts) from Home Assistant, a Shelly button or a Shortcut: the picture is kept and you are told.</>}>
          <div className="stack tight"><CopyField label="Ring" value={`${base}/api/v1/cameras/${cam.id}/ring`} /><CopyField label="Alarm" value={`${base}/api/v1/cameras/${cam.id}/trigger`} /></div>
        </SettingRow>
        <SettingRow title="In Pocket Home" help={homeOk ? 'The camera as a thing in your home: its warranty and manual there, and a note in its log when it stays offline for a day (Settings → Connected apps).'
          : <>Pocket Home is not connected: {home.app?.fix || 'see Settings → Connected apps'}.{!home.app && <CoreHint />}</>}>
          {homeOk ? <Select ariaLabel="Thing" value={cam.home_ref} onChange={v => save({ home_ref: v })} placeholder="Not linked" clearLabel="Not linked" options={(things || []).map(t => ({ value: t.id, label: t.name, hint: [t.room, t.brand].filter(Boolean).join(' · ') }))} />
            : <span className="muted small nowrap">Not connected</span>}
        </SettingRow>
      </section>

      <InfoGrid items={[['Added', ago(cam.created_at)], ['Last picture', cam.last_seen_at ? ago(cam.last_seen_at) : ''], ['Last problem', cam.last_error]]} />
      <div className="row"><button type="button" className="btn danger-text" onClick={() => setDel(true)}><Icon name="trash" size={14} />Remove the camera</button></div>
      {edit && <CameraDialog camera={cam} onClose={() => setEdit(false)} onSaved={onSaved} />}
      {del && <Confirm title={`Remove ${cam.name}?`} message="Its events, pictures and recordings go too." confirmLabel="Remove" onCancel={() => setDel(false)} onConfirm={async () => { setDel(false); try { await api.del(`${V1}/cameras/${cam.id}`); announceChange(); toast('Camera removed'); onDeleted() } catch (e) { toast(errMsg(e), 'err') } }} />}
      <AppHint slug="alerts" scope="alerts:write">Alerts reach your phone through Pocket Alerts (or Pocket Hub, or the push address in Settings → Notifications).</AppHint>
    </div>
  )
}

function ViewerAlerts({ cam, onSaved }: { cam: Camera; onSaved: () => void }) {
  const { toast } = useToast()
  return (
    <SettingRow title={`Alerts from ${cam.name}`} help="When it sees something in the modes it watches, you are told too.">
      <Switch checked={!!cam.notify} onChange={v => api.put(`${V1}/cameras/${cam.id}/notify`, { notify: v }).then(onSaved).catch(e => toast(errMsg(e), 'err'))} label="Alerts" />
    </SettingRow>
  )
}
