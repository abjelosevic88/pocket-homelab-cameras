// The shapes the API sends.

export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = 'core' | 'direct' | ''
export type AppSlug = 'alerts' | 'network' | 'movement' | 'travel' | 'parcels' | 'pets' | 'weather' | 'chat' | 'tasks' | 'notes' | 'photos' | 'files' | 'rooms' | 'home'
type ConnFields = { [K in AppSlug as `${K}_mode`]: ConnMode } & { [K in AppSlug as `${K}_via`]: ConnVia } & { [K in AppSlug as `${K}_detail` | `${K}_url` | `${K}_token` | `${K}_public_url` | `${K}_open_url`]: string } & { [K in AppSlug as `${K}_configured`]: boolean }

export type Mode = 'home' | 'away' | 'night' | 'off'
export type Label = 'person' | 'vehicle' | 'animal' | 'parcel'

export type Settings = ConnFields & {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number
  mode: Mode; mode_at: string; mode_by: string; mode_reason: string; auto_mode: boolean
  night_from: number; night_to: number; away_trips: boolean; away_out: boolean; away_phones: boolean; phones: string[]; home_radius: number
  files_folder: string; notes_folder: string; tasks_list: string; chat_to: string
  weather_calm: boolean; pets_quiet: boolean; home_log: boolean; ai_help: boolean; ai_photos: boolean
  notify_events: boolean; notify_doorbell: boolean; notify_offline: boolean; cooldown: number
  recordings_dir: string; detect_fps: number; storage_cap_gb: number
  notify_url: string; notify_format: string; notify_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string; demo: boolean
}

export type AppStateName = 'on' | 'partly' | 'off' | 'link_off' | 'missing' | 'old' | 'no_core' | 'not_set_up' | 'error'
export interface AppState { slug: AppSlug; name: string; reason: string; mode: ConnMode; via: ConnVia; state: AppStateName; detail: string; fix: string; on: string[]; open_url: string }

export type CameraKind = 'rtsp' | 'mjpeg' | 'snapshot' | 'demo'
export type RecordMode = 'off' | 'events' | 'always'

export interface Zone { name: string; kind: 'watch' | 'ignore'; points: [number, number][] }

export interface CamState { online: boolean; running: boolean; error: string; last_at: string | null; recording: boolean; rec_error: string; event: boolean; watchers: number }

export interface Camera {
  id: number; name: string; kind: CameraKind; brand: string; host: string; username: string; has_password: boolean
  main_url: string; sub_url: string; snapshot_url: string; enabled: boolean; record: RecordMode; retain_days: number; event_days: number
  detect: boolean; sensitivity: number; zones: Zone[]; labels: Label[]; modes: Mode[]; doorbell: boolean; ai_check: boolean
  room: string; room_ref: string; home_ref: string; position: number; notes: string; last_seen_at: string | null; last_error: string
  created_at: string; updated_at: string | null; mine: boolean; owner: string; state: CamState; bytes: number; can_record: boolean
  share_urls?: { snapshot: string; live: string }; notify?: boolean
  last_event?: { id: number; started_at: string; labels: Label[]; summary: string; kind: string; pet: string }
  shares?: Share[]; recorded_from?: string; recorded_to?: string
}

export interface Share { camera_id: number; user_id: number; name: string; email: string; notify: boolean }

export interface CamEvent {
  id: number; camera_id: number; camera: string; kind: 'motion' | 'doorbell' | 'trigger'; started_at: string; ended_at: string | null
  score: number; box: string; labels: Label[]; summary: string; ai_state: '' | 'pending' | 'done' | 'off' | 'failed'; worry: number
  frames: number; best: number; clip: boolean; mode: Mode | ''; alerted: boolean; reviewed: boolean; starred: boolean; nothing: boolean
  pet: string; parcel_ref: string; parcel_text: string; weather: string; note: string; source: string; title: string; url: string
  mine?: boolean; recorded?: boolean; parcel_url?: string; prev_id?: number
}

export interface ModeChange { id: number; mode: Mode; reason: string; by: string; at: string }

export interface ModeInfo { mode: Mode; at: string; by: string; reason: string; auto_mode: boolean; auto: { mode?: Mode; reason?: string }; night_from: number; night_to: number; recent: ModeChange[] }

export interface Home {
  mode: Mode; auto_mode: boolean; online: number; total: number; unreviewed: number
  cameras: { id: number; name: string; enabled: boolean; online: boolean; event: boolean; mine: boolean }[]
  today: Record<string, number>
}

export interface Brand { id: string; name: string; main: string; sub: string; snapshot: string; port: number; note: string }

export interface Found { ip: string; name: string; vendor: string; brand: string; rtsp: boolean; web: boolean; from: string; online: boolean; added: boolean }

export interface TimelineRow { camera: { id: number; name: string; kind: CameraKind; record: RecordMode }; spans: [string, string][]; pieces: { id: number; start: string; end: string }[]; events: CamEvent[] }
export interface Timeline { day: string; from: string; to: string; rows: TimelineRow[]; modes: ModeChange[] }
