// Cameras: the list to manage — add, find on the network, order, switch on and off.
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { announceChange, useOnChange } from '../lib/data'
import { KIND_WORDS, snapshotURL } from '../lib/cams'
import type { Camera } from '../lib/types'
import { ago, fmtBytes } from '../lib/format'
import { Empty, Loading, Switch, useToast, errMsg } from '../components/ui'
import { Icon } from '../components/icons'
import { Dot } from '../components/bits'
import { CameraDialog, DiscoverDialog } from '../components/dialogs'
import { openCreate } from '../components/Layout'

export default function Cameras() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { toast } = useToast()
  const [data, setData] = useState<{ cameras: Camera[]; ffmpeg: boolean; storage: number; storage_cap: number } | null>(null)
  const [find, setFind] = useState(params.get('find') === '1')
  const [prefill, setPrefill] = useState<{ host: string; brand: string } | null>(null)
  const load = useCallback(() => { api.get<typeof data>(`${V1}/cameras`).then(setData).catch(() => {}) }, [])
  useEffect(() => { load() }, [load])
  useOnChange(load)
  useEffect(() => { if (params.get('find')) { params.delete('find'); setParams(params, { replace: true }) } }, [params, setParams])
  const mine = data?.cameras.filter(c => c.mine) || []
  const shared = data?.cameras.filter(c => !c.mine) || []
  const move = async (i: number, d: number) => {
    const ids = mine.map(c => c.id)
    const j = i + d
    if (j < 0 || j >= ids.length) return
    ;[ids[i], ids[j]] = [ids[j], ids[i]]
    await api.put(`${V1}/cameras/order`, { ids }).catch(() => {})
    announceChange(); load()
  }
  const toggle = async (c: Camera, on: boolean) => {
    try { await api.put(`${V1}/cameras/${c.id}`, { enabled: on }); announceChange(); load() } catch (e) { toast(errMsg(e), 'err') }
  }
  if (!data) return <Loading />
  return (
    <div className="stack loose">
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => openCreate()}><Icon name="plus" size={15} />Add a camera</button>
        <button type="button" className="btn" onClick={() => setFind(true)}><Icon name="scan" size={15} />Find cameras on the network</button>
        <Link to="/settings/data" className="btn ghost"><Icon name="upload" size={15} />Bring cameras over from Frigate</Link>
        <span className="grow" />
        <span className="muted small">Recordings: {fmtBytes(data.storage)}{data.storage_cap ? ` of ${fmtBytes(data.storage_cap)}` : ''}</span>
      </div>
      {!data.ffmpeg && <div className="callout warn"><Icon name="alert" size={15} /><div>ffmpeg is not installed on this server: RTSP cameras and recording need it (the container image has it). MJPEG and snapshot cameras work without.</div></div>}
      {!mine.length && !shared.length ? <Empty title="No cameras yet" icon="camera"><p>Add one by its make and address, or find the cameras on your network.</p></Empty> : <>
        {!!mine.length && <div className="cam-list card">
          {mine.map((c, i) => (
            <div key={c.id} className="cam-row">
              <button type="button" className="cam-row-pic" onClick={() => navigate(`/cameras/${c.id}`)} aria-label={`Open ${c.name}`}>{c.enabled ? <img src={snapshotURL(c.id)} alt="" loading="lazy" /> : <Icon name="pause" size={18} />}</button>
              <div className="grow">
                <Link to={`/cameras/${c.id}`} className="cam-row-name">{c.name}</Link>
                <div className="muted small ellipsis"><Dot tone={!c.enabled ? 'off' : c.state.online ? 'ok' : 'bad'} /> {!c.enabled ? 'Switched off' : c.state.online ? 'Online' : `Offline${c.state.error ? ` — ${c.state.error}` : ''}`} · {[KIND_WORDS[c.kind], c.brand, c.room].filter(Boolean).join(' · ')}{c.bytes ? ` · ${fmtBytes(c.bytes)}` : ''}{c.last_event ? ` · last event ${ago(c.last_event.started_at)}` : ''}</div>
              </div>
              <div className="row tight">
                <button type="button" className="btn ghost icon-only sm" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Up" title="Move up"><Icon name="arrow-up" size={14} /></button>
                <button type="button" className="btn ghost icon-only sm" disabled={i === mine.length - 1} onClick={() => move(i, 1)} aria-label="Down" title="Move down"><Icon name="arrow-down" size={14} /></button>
                <Switch checked={c.enabled} onChange={v => toggle(c, v)} label="On" />
              </div>
            </div>
          ))}
        </div>}
        {!!shared.length && <section>
          <h3 className="day-h">Shared with you</h3>
          <div className="cam-list card">
            {shared.map(c => (
              <Link key={c.id} to={`/cameras/${c.id}`} className="cam-row">
                <span className="cam-row-pic"><img src={snapshotURL(c.id)} alt="" loading="lazy" /></span>
                <div className="grow"><span className="cam-row-name">{c.name}</span><div className="muted small">{c.owner}'s · {c.state.online ? 'online' : 'offline'}</div></div>
              </Link>
            ))}
          </div>
        </section>}
      </>}
      {find && <DiscoverDialog onClose={() => setFind(false)} onPick={f => { setFind(false); setPrefill({ host: f.ip, brand: f.brand }) }} />}
      {prefill && <CameraDialog prefill={{ host: prefill.host, brand: prefill.brand }} onClose={() => setPrefill(null)} onSaved={c => navigate(`/cameras/${c.id}`)} />}
    </div>
  )
}
