// The forms that open over a page.
import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import { announceChange, useAppState } from '../lib/data'
import { SCENES } from '../lib/cams'
import type { Brand, Camera, CameraKind, Found } from '../lib/types'
import { Field, Modal, Segmented, Spinner, Switch, useToast, errMsg } from './ui'
import { Select } from './Select'
import { Icon } from './icons'

export function useList<T>(path: string, key: string, on = true): T[] | null {
  const [v, setV] = useState<T[] | null>(null)
  useEffect(() => { if (on) api.get<Record<string, T[]>>(`${V1}${path}`).then(r => setV(r[key] || [])).catch(() => setV([])); else setV([]) }, [path, key, on])
  return v
}

let brandCache: { brands: Brand[]; scenes: string[]; ffmpeg: boolean } | null = null
export function useBrands() {
  const [b, setB] = useState(brandCache)
  useEffect(() => { if (!brandCache) api.get<typeof brandCache>(`${V1}/cameras/brands`).then(r => { brandCache = r; setB(r) }).catch(() => {}) }, [])
  return b
}

interface TestResult { ok: boolean; error?: string; warning?: string; picture?: string; main?: { codec: string; width: number; height: number; fps: number; audio: string }; sub?: { codec: string; width: number; height: number }; sub_error?: string; main_url?: string }

type Form = { name: string; kind: CameraKind; brand: string; host: string; username: string; password: string; main_url: string; sub_url: string; snapshot_url: string; room: string; room_ref: string; doorbell: boolean; scene: string }

/** Add a camera (or change how it is reached): its make and address, a try with a picture, then its name and room. */
export function CameraDialog({ camera, prefill, onClose, onSaved }: { camera?: Camera; prefill?: Partial<Form>; onClose: () => void; onSaved?: (c: Camera) => void }) {
  const { toast } = useToast()
  const brands = useBrands()
  const appState = useAppState()
  const rooms = useList<{ ref: string; name: string; floor: string; plan: string }>('/apps/rooms', 'rooms', appState('rooms', 'rooms:read').ok)
  const editing = !!camera
  const [f, setF] = useState<Form>(() => ({
    name: camera?.name || '', kind: camera?.kind || 'rtsp', brand: camera?.brand || '', host: camera?.host || '', username: camera?.username || 'admin',
    password: camera?.has_password ? '••••' : '', main_url: camera?.main_url?.startsWith('demo:') ? '' : camera?.main_url || '', sub_url: camera?.sub_url || '', snapshot_url: camera?.snapshot_url || '',
    room: camera?.room || '', room_ref: camera?.room_ref || '', doorbell: camera?.doorbell || false, scene: camera?.main_url?.startsWith('demo:') ? camera.main_url.slice(5) : 'door', ...prefill,
  }))
  const [step, setStep] = useState<'make' | 'where' | 'name'>(editing ? 'where' : prefill?.brand !== undefined || prefill?.host ? 'where' : 'make')
  const [advanced, setAdvanced] = useState(editing && !f.brand)
  const [test, setTest] = useState<TestResult | null>(null)
  const [testing, setTesting] = useState(false)
  const [busy, setBusy] = useState(false)
  const set = (p: Partial<Form>) => { setF(x => ({ ...x, ...p })); setTest(null) }
  const brand = brands?.brands.find(b => b.id === f.brand)
  const needsURL = f.kind !== 'demo' && (!brand || !brand.main || advanced)

  const body = () => {
    const b: Record<string, unknown> = { name: f.name || 'Camera', kind: f.kind, brand: f.brand, host: f.host.trim(), username: f.username.trim(), password: f.password, room: f.room, room_ref: f.room_ref, doorbell: f.doorbell }
    if (f.kind === 'demo') b.main_url = 'demo:' + f.scene
    else if (needsURL || editing) { b.main_url = f.main_url.trim(); b.sub_url = f.sub_url.trim(); b.snapshot_url = f.snapshot_url.trim() }
    if (editing && !advanced && brand?.main && !f.main_url) { b.main_url = ''; b.sub_url = ''; b.snapshot_url = '' }
    return b
  }
  const tryIt = async () => {
    setTesting(true); setTest(null)
    try { setTest(await api.post<TestResult>(`${V1}/cameras/test`, { ...body(), id: camera?.id })) } catch (e) { setTest({ ok: false, error: errMsg(e) }) } finally { setTesting(false) }
  }
  const save = async () => {
    setBusy(true)
    try {
      const c = editing ? await api.put<Camera>(`${V1}/cameras/${camera!.id}`, body()) : await api.post<Camera>(`${V1}/cameras`, body())
      toast(editing ? 'Saved' : `${c.name} added`); announceChange(); onSaved?.(c); onClose()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const pickBrand = (id: string, kind: CameraKind = 'rtsp') => {
    const b = brands?.brands.find(x => x.id === id)
    set({ brand: id, kind, main_url: '', sub_url: '', snapshot_url: '' })
    setAdvanced(!b?.main)
    setStep('where')
  }
  const canGoOn = f.kind === 'demo' || (needsURL ? !!(f.main_url || f.snapshot_url) : !!f.host)

  return (
    <Modal title={editing ? `How ${camera!.name} is reached` : 'Add a camera'} onClose={onClose} size="lg" footer={
      <div className="form-actions">
        {step !== 'make' && !editing && <button type="button" className="btn ghost" onClick={() => setStep(step === 'name' ? 'where' : 'make')}><Icon name="chevron-left" size={15} />Back</button>}
        <span className="grow" />
        <button type="button" className="btn" onClick={onClose}>Cancel</button>
        {step === 'where' && !editing && <button type="button" className="btn primary" disabled={!canGoOn} onClick={() => setStep('name')}>Next<Icon name="chevron-right" size={15} /></button>}
        {(step === 'name' || editing) && <button type="button" className="btn primary" disabled={busy || (!editing && !f.name.trim())} onClick={save}>{editing ? 'Save' : 'Add the camera'}</button>}
      </div>
    }>
      {step === 'make' && <div className="stack">
        <p className="muted">Which make is it? Pocket Cameras knows where most cameras send their video.</p>
        <div className="brand-grid">
          {(brands?.brands || []).map(b => (
            <button key={b.id} type="button" className={`brand-btn ${f.brand === b.id ? 'on' : ''}`} onClick={() => pickBrand(b.id)}>
              <Icon name="cctv" size={18} /><span>{b.name}</span>
            </button>
          ))}
          <button type="button" className="brand-btn" onClick={() => { set({ brand: '', kind: 'rtsp' }); setAdvanced(true); setStep('where') }}><Icon name="link" size={18} /><span>I have its address</span></button>
          <button type="button" className="brand-btn" onClick={() => { set({ brand: '', kind: 'demo', name: SCENES[f.scene] }); setStep('where') }}><Icon name="sparkles" size={18} /><span>A demo camera</span></button>
        </div>
        {brands && !brands.ffmpeg && <div className="callout warn"><Icon name="alert" size={15} /><div>ffmpeg is not installed here, so only cameras with an MJPEG stream or a snapshot address work. The container image has it.</div></div>}
      </div>}

      {step === 'where' && <div className="stack">
        {f.kind === 'demo' ? (
          <Field group label="Which picture" help="A demo camera draws its own picture with people, couriers, cars and cats passing — to try everything without a camera.">
            <Segmented value={f.scene} onChange={v => set({ scene: v, name: f.name && !Object.values(SCENES).includes(f.name) ? f.name : SCENES[v] })} options={Object.entries(SCENES).map(([k, v]) => ({ value: k, label: v }))} />
          </Field>
        ) : <>
          {brand?.note && <div className="callout info"><Icon name="info" size={15} /><div>{brand.note}</div></div>}
          {brand?.main && !advanced && <>
            <div className="cols2">
              <Field label="Its address on your network" help="The IP address, like 192.168.1.50 — in your router's list, the camera's app, or Find cameras."><input value={f.host} onChange={e => set({ host: e.target.value })} placeholder="192.168.1.50" autoFocus inputMode="decimal" /></Field>
              <span />
            </div>
            <div className="cols2">
              <Field label="User name"><input value={f.username} onChange={e => set({ username: e.target.value })} autoComplete="off" /></Field>
              <Field label="Password"><input type="password" value={f.password} onFocus={e => { if (e.target.value.includes('•')) set({ password: '' }) }} onChange={e => set({ password: e.target.value })} autoComplete="new-password" /></Field>
            </div>
          </>}
          {(needsURL || advanced) && <>
            <Field group label="What it sends">
              <Segmented size="sm" value={f.kind} onChange={v => set({ kind: v as CameraKind })} options={[{ value: 'rtsp', label: 'Video (RTSP)' }, { value: 'mjpeg', label: 'MJPEG stream' }, { value: 'snapshot', label: 'Pictures (snapshot)' }]} />
            </Field>
            {f.kind !== 'snapshot' && <Field label={f.kind === 'rtsp' ? 'Full-quality stream (recorded)' : 'Stream address'} help={f.kind === 'rtsp' ? 'Starts with rtsp:// or rtsps://. A password in the address is kept and hidden.' : 'An address that sends multipart/x-mixed-replace JPEGs.'}>
              <input value={f.main_url} onChange={e => set({ main_url: e.target.value })} placeholder={f.kind === 'rtsp' ? 'rtsp://192.168.1.50:554/stream1' : 'http://192.168.1.50/video.mjpg'} autoComplete="off" spellCheck={false} />
            </Field>}
            {f.kind === 'rtsp' && <Field label="Small stream (optional)" help="Watched and checked for movement; much lighter than the full one. Empty: the full stream."><input value={f.sub_url} onChange={e => set({ sub_url: e.target.value })} placeholder="rtsp://192.168.1.50:554/stream2" autoComplete="off" spellCheck={false} /></Field>}
            {(f.kind === 'snapshot' || advanced) && <Field label="Snapshot address" help={f.kind === 'snapshot' ? 'An address that answers with one JPEG picture, asked about once a second.' : 'Optional.'}><input value={f.snapshot_url} onChange={e => set({ snapshot_url: e.target.value })} placeholder="http://192.168.1.50/snapshot.jpg" autoComplete="off" spellCheck={false} /></Field>}
            <div className="cols2">
              <Field label="User name"><input value={f.username} onChange={e => set({ username: e.target.value })} autoComplete="off" /></Field>
              <Field label="Password"><input type="password" value={f.password} onFocus={e => { if (e.target.value.includes('•')) set({ password: '' }) }} onChange={e => set({ password: e.target.value })} autoComplete="new-password" /></Field>
            </div>
          </>}
          {brand?.main && <button type="button" className="conn-advanced" onClick={() => setAdvanced(a => !a)}>{advanced ? `Use the usual ${brand.name} addresses` : 'Type the addresses yourself'}</button>}
        </>}
        <div className="row wrap">
          <button type="button" className="btn" onClick={tryIt} disabled={testing || !canGoOn}>{testing ? <Spinner /> : <Icon name="play" size={14} />}Try it</button>
          {testing && <span className="muted small">Asking the camera for a picture (up to 20 seconds)…</span>}
          {test && !test.ok && <span className="bad small">✕ {test.error}</span>}
          {test?.ok && <span className="ok small">✓ The camera answered{test.main ? ` — ${test.main.width}×${test.main.height}, ${test.main.codec.toUpperCase()}${test.main.fps ? `, ${test.main.fps} fps` : ''}${test.main.audio ? ', with sound' : ''}` : ''}</span>}
        </div>
        {test?.warning && <div className="callout warn"><Icon name="alert" size={15} /><div>{test.warning}</div></div>}
        {test?.sub_error && <p className="muted small">The small stream did not answer ({test.sub_error}); the full one is used for everything.</p>}
        {test?.picture && <img className="test-pic" src={test.picture} alt="What the camera sees" />}
        {editing && <NameFields f={f} set={set} rooms={rooms} />}
      </div>}

      {step === 'name' && <div className="stack">
        {test?.picture && <img className="test-pic small" src={test.picture} alt="" />}
        <NameFields f={f} set={set} rooms={rooms} autoFocus />
        <p className="muted small">What it records, when it alerts and the parts of the picture to watch are on the camera's page once it is added.</p>
      </div>}
    </Modal>
  )
}

function NameFields({ f, set, rooms, autoFocus }: { f: Form; set: (p: Partial<Form>) => void; rooms: { ref: string; name: string; floor: string; plan: string }[] | null; autoFocus?: boolean }) {
  return <>
    <div className="cols2">
      <Field label="Name"><input value={f.name} onChange={e => set({ name: e.target.value })} placeholder="Front door" autoFocus={autoFocus} maxLength={60} /></Field>
      <Field label="Where" help={rooms?.length ? 'A room from Pocket Rooms, or type one.' : 'A room or place, for you.'}>
        {rooms?.length ? <Select ariaLabel="Room" value={f.room_ref || f.room} onChange={v => { const r = rooms.find(x => x.ref === v); set(r ? { room: r.name, room_ref: r.ref } : { room: v, room_ref: '' }) }} placeholder="Choose a room" clearLabel="None"
          options={[...rooms.map(r => ({ value: r.ref, label: r.name, hint: [r.floor, r.plan].filter(Boolean).join(' · ') })), ...(f.room && !f.room_ref ? [{ value: f.room, label: f.room }] : [])]} onCreate={v => v} createLabel="Use" />
          : <input value={f.room} onChange={e => set({ room: e.target.value, room_ref: '' })} placeholder="Hallway" />}
      </Field>
    </div>
    <Switch checked={f.doorbell} onChange={v => set({ doorbell: v })} label="This is a doorbell: a ring always reaches my phone" />
  </>
}

/** Cameras on the home network (Pocket Network, or a scan of an address range). */
export function DiscoverDialog({ onClose, onPick }: { onClose: () => void; onPick: (f: Found) => void }) {
  const appState = useAppState()
  const net = appState('network', 'network:read')
  const [subnet, setSubnet] = useState(() => localStorage.getItem('pcam-subnet') || '')
  const [res, setRes] = useState<{ found: Found[]; note: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const run = async (withScan: boolean) => {
    setBusy(true); setErr('')
    if (withScan) localStorage.setItem('pcam-subnet', subnet)
    try { setRes(await api.post(`${V1}/cameras/discover`, { subnet: withScan ? subnet : '' })) } catch (e) { setErr(errMsg(e)) } finally { setBusy(false) }
  }
  useEffect(() => { if (net.ok) run(false) }, [net.ok]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Modal title="Find cameras on your network" onClose={onClose} size="lg">
      <div className="stack">
        <p className="muted">{net.ok ? 'Pocket Network knows the devices on your network; cameras among them are listed below. You can also look through a range of addresses, or check one.' : 'Look through a range of addresses for cameras (anything answering on the RTSP port), or check one address. With Pocket Network connected, the cameras it knows are listed at once.'}</p>
        <div className="row">
          <input value={subnet} onChange={e => setSubnet(e.target.value)} placeholder="192.168.1.0/24 or one address" aria-label="Address range or one address" style={{ maxWidth: 240 }} />
          <button type="button" className="btn" disabled={busy || !subnet.trim()} onClick={() => run(true)}>{busy ? <Spinner /> : <Icon name="scan" size={14} />}Look</button>
          {busy && <span className="muted small">Up to 40 seconds…</span>}
        </div>
        {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
        {res?.note && <div className="callout"><Icon name="alert" size={15} /><span>{res.note}</span></div>}
        {res && (res.found.length ? <div className="found-list">
          {res.found.map(x => (
            <div key={x.ip} className="found-row">
              <Icon name="cctv" size={18} />
              <div className="grow"><div><b>{x.name || x.ip}</b>{x.brand && <span className="badge">{x.brand}</span>}{x.added && <span className="badge ok">added</span>}</div>
                <div className="muted small">{[x.ip, x.vendor, x.rtsp ? 'RTSP' : '', x.from === 'network' ? 'from Pocket Network' : 'found by looking'].filter(Boolean).join(' · ')}</div></div>
              <button type="button" className="btn sm" disabled={x.added} onClick={() => onPick(x)}>Add</button>
            </div>
          ))}
        </div> : <p className="muted">Nothing found{subnet.includes('/') ? ' in that range' : ''}.</p>)}
      </div>
    </Modal>
  )
}
