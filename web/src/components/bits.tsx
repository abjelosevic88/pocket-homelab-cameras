// Small pieces used on many pages.
import React, { useEffect, useRef, useState } from 'react'
import { Icon } from './icons'
import { LABEL, MODES, mainLabel, snapshotURL, liveURL, liveVideoURL } from '../lib/cams'
import type { CamEvent, Label, Mode } from '../lib/types'
import { useAppState, openAction } from '../lib/data'
import { useToast, CoreHint } from './ui'
import { api } from '../lib/api'
import { mediaSource, playLive } from '../lib/livevideo'

/** person / vehicle / parcel / animal chips. */
export function LabelChips({ labels, pet, small }: { labels: Label[]; pet?: string; small?: boolean }) {
  if (!labels.length) return null
  return <span className="chips">{labels.map(l => <span key={l} className={`chip tone-${LABEL[l]?.tone || 'motion'} ${small ? 'sm' : ''}`}><Icon name={LABEL[l]?.icon || 'activity'} size={small ? 11 : 12} />{l === 'animal' && pet ? pet : LABEL[l]?.label || l}</span>)}</span>
}

/** The round icon of an event's main subject. */
export function EventIcon({ e, size = 30 }: { e: Pick<CamEvent, 'labels' | 'kind' | 'worry' | 'nothing'>; size?: number }) {
  const m = mainLabel(e)
  return <span className={`ev-ico tone-${e.nothing ? 'off' : e.worry >= 1 ? 'worry' : m.tone}`} style={{ width: size, height: size }} title={m.label}><Icon name={m.icon} size={Math.round(size * 0.52)} /></span>
}

/** Home / Away / Night / Off as four big buttons. */
export function ModeSwitch({ value, onChange, busy, size = '' }: { value: Mode; onChange: (m: Mode) => void; busy?: boolean; size?: string }) {
  return (
    <div className={`mode-switch ${size}`} role="radiogroup" aria-label="Mode">
      {MODES.map(m => (
        <button key={m.id} type="button" role="radio" aria-checked={value === m.id} className={`mode-btn mode-${m.id} ${value === m.id ? 'on' : ''}`} disabled={busy} title={m.sub} onClick={() => onChange(m.id)}>
          <Icon name={m.icon} size={18} /><span>{m.label}</span>
        </button>
      ))}
    </div>
  )
}

/** Is the element on screen (and the tab visible)? */
function useVisible(ref: React.RefObject<Element | null>) {
  const [vis, setVis] = useState(true)
  const [tab, setTab] = useState(document.visibilityState === 'visible')
  useEffect(() => {
    const el = ref.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver(es => setVis(es.some(x => x.isIntersecting)), { rootMargin: '100px' })
    io.observe(el)
    return () => io.disconnect()
  }, [ref])
  useEffect(() => {
    const h = () => setTab(document.visibilityState === 'visible')
    document.addEventListener('visibilitychange', h)
    return () => document.removeEventListener('visibilitychange', h)
  }, [])
  return vis && tab
}

/** A camera's newest picture, refreshed every `every` ms while on screen; the
 * next picture is loaded first, so the tile never flickers. */
export function LivePicture({ id, every = 1500, alt = '', className = '', paused }: { id: number; every?: number; alt?: string; className?: string; paused?: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  const visible = useVisible(box)
  const [src, setSrc] = useState(() => snapshotURL(id, Date.now()))
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    if (!visible || paused) return
    let alive = true
    let timer = 0
    const next = () => {
      const url = snapshotURL(id, Date.now())
      const img = new Image()
      img.onload = () => { if (!alive) return; setSrc(url); setFailed(false); timer = window.setTimeout(next, every) }
      img.onerror = () => { if (!alive) return; setFailed(true); timer = window.setTimeout(next, Math.max(every, 5000)) }
      img.src = url
    }
    timer = window.setTimeout(next, every)
    return () => { alive = false; window.clearTimeout(timer) }
  }, [id, every, visible, paused])
  return (
    <div ref={box} className={`live-pic ${className}`}>
      <img src={src} alt={alt} onError={() => setFailed(true)} onLoad={() => setFailed(false)} draggable={false} />
      {failed && <div className="live-pic-off"><Icon name="wifi-off" size={22} /><span>No picture</span></div>}
    </div>
  )
}

/** The live MJPEG stream; reconnects when the tab comes back. */
export function LiveStream({ id, hd, smooth, alt = '', onError }: { id: number; hd?: boolean; smooth?: boolean; alt?: string; onError?: () => void }) {
  const [n, setN] = useState(0)
  const [visible, setVisible] = useState(document.visibilityState === 'visible')
  useEffect(() => {
    const h = () => { const v = document.visibilityState === 'visible'; setVisible(v); if (v) setN(x => x + 1) }
    document.addEventListener('visibilitychange', h)
    return () => document.removeEventListener('visibilitychange', h)
  }, [])
  if (!visible) return <div className="live-pic"><img src={snapshotURL(id, n)} alt={alt} /></div>
  return <div className="live-pic"><img key={`${n}-${hd ? 1 : 0}`} src={`${liveURL(id, hd, smooth)}&n=${n}`} alt={alt} onError={() => { onError?.(); window.setTimeout(() => setN(x => x + 1), 4000) }} draggable={false} /></div>
}

/** An RTSP camera's live video as the camera sends it (its own rate, little
 *  delay); pictures instead where the browser cannot play it. */
export function LiveVideo({ id, hd, alt = '' }: { id: number; hd?: boolean; alt?: string }) {
  const ref = useRef<HTMLVideoElement>(null)
  const [pictures, setPictures] = useState(() => !mediaSource())
  const [playing, setPlaying] = useState(false)
  const [n, setN] = useState(0)
  const [visible, setVisible] = useState(document.visibilityState === 'visible')
  useEffect(() => {
    const h = () => setVisible(document.visibilityState === 'visible')
    document.addEventListener('visibilitychange', h)
    return () => document.removeEventListener('visibilitychange', h)
  }, [])
  const fails = useRef(0)
  useEffect(() => { setPictures(!mediaSource()); fails.current = 0 }, [id, hd])
  useEffect(() => {
    const v = ref.current
    if (pictures || !visible || !v) return
    setPlaying(false)
    let again = 0
    const stop = playLive(v, `${liveVideoURL(id, hd)}&n=${n}`, s => {
      if (s === 'playing') { setPlaying(true); fails.current = 0 }
      else if (s === 'unsupported') setPictures(true)
      else if (s === 'failed' && ++fails.current >= 3) setPictures(true) // the camera was busy or slow three times running
      else { setPlaying(false); again = window.setTimeout(() => setN(x => x + 1), s === 'failed' ? 1500 : 1000) }
    })
    return () => { stop(); window.clearTimeout(again) }
  }, [id, hd, pictures, visible, n])
  if (pictures) return <LiveStream id={id} hd={hd} smooth alt={alt} />
  return (
    <div className="live-pic">
      <video ref={ref} muted autoPlay playsInline disablePictureInPicture aria-label={alt} />
      {!playing && <img className="live-poster" src={snapshotURL(id, n + 1)} alt={alt} draggable={false} />}
    </div>
  )
}

/** A camera's online dot. */
export function Dot({ tone, title }: { tone: 'ok' | 'bad' | 'off' | 'move'; title?: string }) {
  return <span className={`dot-state tone-${tone}`} title={title} />
}

/** A button that sends something to another Pocket app — greyed with the reason when that app is not connected. */
export function AppButton({ slug, scope, label, icon, path, body, onDone, className = 'btn sm', confirm }: {
  slug: string; scope?: string; label: string; icon: string; path: string; body?: unknown; onDone?: (r: { url?: string; text?: string }) => void; className?: string; confirm?: () => unknown
}) {
  const state = useAppState()
  const { ok, app, loading } = state(slug, scope)
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    try {
      const b = confirm ? confirm() : body
      const r = await api.post<{ url?: string; text?: string }>(path, b ?? {})
      toast(r.text || 'Done', 'ok', openAction(r.url))
      onDone?.(r)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const why = !ok && !loading ? (app ? `${app.name}: ${app.detail || app.fix || 'not connected'}` : 'Needs Pocket Core or a direct connection (Settings → Connected apps)') : label
  return <button type="button" className={className} disabled={!ok || busy} title={why} onClick={run}><Icon name={icon} size={14} />{label}</button>
}

export function AppHint({ slug, scope, children }: { slug: string; scope?: string; children?: React.ReactNode }) {
  const state = useAppState()
  const { ok, app, loading } = state(slug, scope)
  if (ok || loading) return null
  return <div className="app-hint muted small"><Icon name="link" size={13} />{children || <>{app?.name || 'That app'} is not connected: {app?.fix || 'see Settings → Connected apps'}.</>}{!app && <CoreHint />}</div>
}

export function InfoGrid({ items }: { items: [React.ReactNode, React.ReactNode][] }) {
  const shown = items.filter(([, v]) => v !== '' && v !== null && v !== undefined && v !== false)
  if (!shown.length) return null
  return <dl className="info-grid">{shown.map(([k, v], i) => <React.Fragment key={i}><dt>{k}</dt><dd>{v}</dd></React.Fragment>)}</dl>
}
