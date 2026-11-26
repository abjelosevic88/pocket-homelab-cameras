// Words, icons and colours for modes and what the cameras see.
import type { CamEvent, Label, Mode } from './types'
import { V1 } from './api'

export const MODES: { id: Mode; label: string; icon: string; sub: string }[] = [
  { id: 'home', label: 'Home', icon: 'home', sub: 'Someone is home: only the cameras set to alert at home do.' },
  { id: 'away', label: 'Away', icon: 'plane', sub: 'Nobody is home: the cameras watch.' },
  { id: 'night', label: 'Night', icon: 'moon', sub: 'Everyone is asleep: outside cameras watch.' },
  { id: 'off', label: 'Off', icon: 'pause', sub: 'Alerts paused (the doorbell still rings). Recording goes on.' },
]
export const MODE: Record<string, { label: string; icon: string }> = Object.fromEntries(MODES.map(m => [m.id, m]))

export const LABELS: { id: Label; label: string; many: string; icon: string; tone: string }[] = [
  { id: 'person', label: 'Person', many: 'People', icon: 'user', tone: 'person' },
  { id: 'vehicle', label: 'Vehicle', many: 'Vehicles', icon: 'car', tone: 'vehicle' },
  { id: 'parcel', label: 'Parcel', many: 'Parcels', icon: 'package', tone: 'parcel' },
  { id: 'animal', label: 'Animal', many: 'Animals', icon: 'paw', tone: 'animal' },
]
export const LABEL: Record<string, { label: string; many: string; icon: string; tone: string }> = Object.fromEntries(LABELS.map(l => [l.id, l]))

/** The main thing an event was about. */
export function mainLabel(e: Pick<CamEvent, 'labels' | 'kind'>): { label: string; icon: string; tone: string } {
  if (e.kind === 'doorbell') return { label: 'Doorbell', icon: 'bell', tone: 'doorbell' }
  if (e.kind === 'trigger') return { label: 'Alarm', icon: 'alert', tone: 'worry' }
  for (const l of ['parcel', 'person', 'vehicle', 'animal']) if (e.labels.includes(l as Label)) return LABEL[l]
  return { label: 'Movement', icon: 'activity', tone: 'motion' }
}

export const pictureURL = (e: { id: number }, n?: number) => `${V1}/events/${e.id}/picture.jpg${n ? `?n=${n}` : ''}`
export const snapshotURL = (id: number, t?: number) => `${V1}/cameras/${id}/snapshot.jpg${t ? `?t=${t}` : ''}`
/** hd: the full-quality stream; smooth: a feed of its own from the small stream (RTSP cameras), else the few pictures a second the camera is checked with. */
export const liveURL = (id: number, hd = false, smooth = false) => `${V1}/cameras/${id}/live.mjpg?${hd ? 'hd=1' : smooth ? 'smooth=1' : 'q=0'}`

/** An RTSP camera's video as it is (fragmented MP4): the full-quality stream with hd. */
export const liveVideoURL = (id: number, hd = false) => `${V1}/cameras/${id}/live.mp4?hd=${hd ? 1 : 0}`

export const SCENES: Record<string, string> = { door: 'Front door', drive: 'Driveway', garden: 'Back garden', room: 'Living room' }

export const KIND_WORDS: Record<string, string> = { rtsp: 'RTSP stream', mjpeg: 'MJPEG stream', snapshot: 'Snapshot address', demo: 'Demo camera' }

/** "12 s", "1 min 20 s" */
export function eventLength(e: Pick<CamEvent, 'started_at' | 'ended_at'>): string {
  if (!e.ended_at) return ''
  const s = Math.max(1, Math.round((Date.parse(e.ended_at) - Date.parse(e.started_at)) / 1000))
  if (s < 60) return `${s} s`
  return `${Math.floor(s / 60)} min${s % 60 ? ` ${s % 60} s` : ''}`
}
