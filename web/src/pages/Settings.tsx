import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, AppSlug, AppState, ConnMode, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CoreHint, Field, Loading, Modal, Segmented, SettingRow, Switch, Tabs, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { PocketModelSelect } from '../components/PocketModelSelect'
import { APP_INFO, STATE_WORDS } from '../lib/apps'
import { useAppState } from '../lib/data'

type TabId = 'general' | 'notifications' | 'apps' | 'ai' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'general', label: 'General', icon: 'settings', sub: 'Home, Away and Night: switching by itself — and dates and the app title.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'What reaches your phone: events in the modes each camera watches, the doorbell, a camera going offline.' },
  { id: 'apps', label: 'Connected apps', icon: 'link', sub: 'Alerts, Network, Movement, Travel, Parcels, Pets, Weather, Chat, Tasks, Notes, Photos, Files, Rooms and Home.' },
  { id: 'ai', label: 'AI', icon: 'sparkles', sub: 'Who looks at each event: Pocket AI through Core, or your own OpenAI key.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box. Each person has their own cameras; share one from its page.' },
  { id: 'tokens', label: 'API & shortcuts', icon: 'key', sub: 'Personal tokens for the doorbell webhook, Home Assistant, Shortcuts and scripts.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Export everything, bring an export or a Frigate config in, download the database.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const tab: TabId = TABS.find(t => t.id === raw)?.id || 'general'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back" title="Back (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title"><h2>{current.label}</h2><p>{current.sub}</p></div>
            {tab === 'general' && <General />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'apps' && <Integrations />}
            {tab === 'ai' && <AITab />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${(e as Error).message}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok' : test[which]?.startsWith('✕') ? 'bad' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

const HOURS = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: `${String(h).padStart(2, '0')}:00` }))

function General() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  const appState = useAppState()
  const net = appState('network', 'network:read').ok
  const { data: owners } = useAsync(() => net ? api.get<{ owners: string[]; home: string[] }>(`${V1}/apps/network/owners`).catch(() => ({ owners: [], home: [] })) : Promise.resolve({ owners: [] as string[], home: [] as string[] }), [net])
  if (!f) return <Loading />
  const travel = appState('travel', 'trips:read').ok, movement = appState('movement', 'movement:read').ok
  return (
    <div className="stack loose">
      <Section title="Home, Away, Night" icon="shield" sub="Each camera alerts in the modes you choose on its page. Set the mode on the Live page — or let it switch by itself.">
        <SettingRow title="Switch by itself" help="The mode follows the night hours and, below, trips, where you are and the phones at home. A mode you set by hand stays until that picture changes."><Switch checked={f.auto_mode} onChange={v => setF({ ...f, auto_mode: v })} label="Auto" /></SettingRow>
        <SettingRow title="Night" help="Night mode between these hours (when someone is home).">
          <div className="row tight"><Select ariaLabel="From" value={String(f.night_from)} onChange={v => setF({ ...f, night_from: Number(v) })} options={HOURS} /><span className="muted">to</span><Select ariaLabel="To" value={String(f.night_to)} onChange={v => setF({ ...f, night_to: Number(v) })} options={HOURS} /></div>
        </SettingRow>
        <SettingRow title="Away while I am on a trip" help={travel ? 'A trip in Pocket Travel that is on today.' : 'Needs Pocket Travel (Connected apps).'}><Switch checked={f.away_trips} disabled={!travel && !f.away_trips} onChange={v => setF({ ...f, away_trips: v })} label="Trips" /></SettingRow>
        <SettingRow title="Away when I leave home" help={movement ? 'Pocket Movement says where you are; Home again when you are back.' : 'Needs Pocket Movement (Connected apps).'}><Switch checked={f.away_out} disabled={!movement && !f.away_out} onChange={v => setF({ ...f, away_out: v })} label="Movement" /></SettingRow>
        <SettingRow title="Away when nobody's phone is home" help={net ? `Pocket Network sees which phones are on the home Wi-Fi${owners?.home?.length ? ` (now: ${owners.home.join(', ')})` : ''}.` : 'Needs Pocket Network (Connected apps).'}><Switch checked={f.away_phones} disabled={!net && !f.away_phones} onChange={v => setF({ ...f, away_phones: v })} label="Phones" /></SettingRow>
        {f.away_phones && net && !!owners?.owners.length && <SettingRow title="Whose phones count" help="Empty: anyone's.">
          <div className="chips">{owners.owners.map(o => <button key={o} type="button" className={`chip-toggle ${f.phones.includes(o) ? 'on' : ''}`} onClick={() => setF({ ...f, phones: f.phones.includes(o) ? f.phones.filter(x => x !== o) : [...f.phones, o] })}>{o}</button>)}</div>
        </SettingRow>}
        {f.away_out && f.away_phones && <p className="muted small">With both on, the house is Away only when both agree.</p>}
      </Section>
      <Section title="Dates" icon="calendar">
        <SettingRow title="Week starts on"><Segmented size="sm" value={f.week_start} onChange={v => setF({ ...f, week_start: v })} options={[{ value: 1, label: 'Monday' }, { value: 6, label: 'Saturday' }, { value: 7, label: 'Sunday' }]} /></SettingRow>
        <SettingRow title="Date format"><Select ariaLabel="Date format" value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select ariaLabel="Time format" value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        {isAdmin && <SettingRow title="App title" help="Shown in the sidebar for everyone on this box."><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} /></SettingRow>}
      </Section>
      <Section title="This server" icon="server" sub={<>Recordings go to <code>{f.recordings_dir}</code>{f.storage_cap_gb ? `, at most ${f.storage_cap_gb} GB (the oldest go first)` : ' (no cap: set RECORDINGS_MAX_GB)'}. Each camera is checked for movement {f.detect_fps} times a second (DETECT_FPS).</>} />
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Where messages go: an Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/cameras" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
      </Section>
      <Section title="What to push" icon="bell" sub="Through Pocket Alerts when it is connected (it reaches your phone), else Pocket Hub on your phone, and the address above when you set one.">
        <SettingRow title="What the cameras see" help="Each camera says what (people, vehicles, parcels, animals) and in which modes, on its page."><Switch checked={f.notify_events} onChange={v => setF({ ...f, notify_events: v })} label="Events" /></SettingRow>
        <SettingRow title="The doorbell" help="Always, in every mode."><Switch checked={f.notify_doorbell} onChange={v => setF({ ...f, notify_doorbell: v })} label="Doorbell" /></SettingRow>
        <SettingRow title="A camera goes offline (and is back)"><Switch checked={f.notify_offline} onChange={v => setF({ ...f, notify_offline: v })} label="Offline" /></SettingRow>
        <SettingRow title="At most one alert per camera every" help="Something urgent always goes through."><div className="input-suffix" style={{ maxWidth: 140 }}><input type="number" min={0} max={120} value={f.cooldown} onChange={e => setF({ ...f, cooldown: Number(e.target.value) })} /><span>min</span></div></SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test push</button>
        {status('notify')}
      </div>
    </div>
  )
}

const APPS: { slug: AppSlug; title: string; sub: string; token: string; port: string }[] = [
  { slug: 'alerts', title: 'Pocket Alerts — your phone', sub: 'Someone at the door, a parcel, a car on the drive, a camera offline — in your alerts inbox and on your phone.', token: 'Pocket Alerts → Settings → API tokens', port: '8217' },
  { slug: 'network', title: 'Pocket Network — find cameras, who is home', sub: 'Lists the cameras on your network to add; Away when nobody\'s phone is on the home Wi-Fi.', token: 'Pocket Network → Settings → API tokens', port: '8220' },
  { slug: 'movement', title: 'Pocket Movement — away when you leave', sub: 'Away when you leave home, Home when you are back.', token: 'Pocket Movement → Settings → API tokens', port: '8196' },
  { slug: 'travel', title: 'Pocket Travel — away on a trip', sub: 'Away for as long as a trip is on.', token: 'Pocket Travel → Settings → API tokens', port: '8198' },
  { slug: 'parcels', title: 'Pocket Parcels — which parcel came', sub: 'A courier at the door: the parcel it probably was, with a link to it.', token: 'Pocket Parcels → Settings → API tokens', port: '8214' },
  { slug: 'pets', title: 'Pocket Pets — your own animals', sub: 'The AI knows your pets by name, and they do not set off alerts.', token: 'Pocket Pets → Settings → API tokens', port: '8232' },
  { slug: 'weather', title: 'Pocket Weather — wind and rain', sub: 'The weather on each event; in a storm, movement alone does not alert.', token: 'Pocket Weather → Settings → API tokens', port: '8239' },
  { slug: 'chat', title: 'Pocket Chat — share an event', sub: 'A picture or clip of an event to the household chat.', token: 'Pocket Chat → Settings → API tokens', port: '8247' },
  { slug: 'tasks', title: 'Pocket Tasks — something to do', sub: 'A task from an event: fix the gate, ask the neighbour.', token: 'Pocket Tasks → Settings → API tokens', port: '8181' },
  { slug: 'notes', title: 'Pocket Notes — write it down', sub: 'An event with its picture, time and camera, for the insurance or the police.', token: 'Pocket Notes → Settings → API tokens', port: '8175' },
  { slug: 'photos', title: 'Pocket Photos — keep a picture', sub: 'The fox in the garden, in your photo library.', token: 'Pocket Photos → Settings → API tokens', port: '8188' },
  { slug: 'files', title: 'Pocket Files — keep a clip', sub: 'An event\'s clip and picture in your files.', token: 'Pocket Files → Settings → API tokens', port: '8187' },
  { slug: 'rooms', title: 'Pocket Rooms — where it is', sub: 'Choose a camera\'s room from your floor plans.', token: 'Pocket Rooms → Settings → API tokens', port: '8265' },
  { slug: 'home', title: 'Pocket Home — the camera as a thing', sub: 'Link a camera to its entry in your home; a note in its log when it stays offline for a day.', token: 'Pocket Home → Settings → API tokens', port: '8229' },
]
function Integrations() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const { data: states } = useAsync(() => api.get<{ apps: AppState[] }>(`${V1}/apps`).then(r => r.apps).catch(() => [] as AppState[]), [APPS.map(a => f?.[`${a.slug}_mode`]).join()])
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const eff = (m: ConnMode): ConnMode => !coreOn && m === 'core' ? 'off' : m
  const set = (k: string, v: string) => setF({ ...f, [k]: v } as SettingsT)
  return (
    <div className="stack loose">
      <div className="callout info"><Icon name="info" size={15} /><div>These connections are yours. Cameras reads from them to decide the mode and what an event was, and adds things to them when you press a button; everything here works without them. Through <b>Pocket Core</b> Cameras acts as you in your own Pocket apps with nothing to paste (switch it off for yourself in Pocket Core → Settings → Apps acting for me); <b>Direct</b> uses a URL and an API token you enter.{!coreOn && <CoreHint />}</div></div>
      {APPS.map(a => {
        const mode = f[`${a.slug}_mode`]
        const via = f[`${a.slug}_via`]
        const name = APP_INFO[a.slug].name
        const st = states?.find(x => x.slug === a.slug)
        return (
          <Section key={a.slug} title={a.title} icon={APP_INFO[a.slug].icon} actions={st && st.state !== 'on' && st.state !== 'off' ? <span className={`badge ${st.state === 'partly' ? 'ok' : 'warn'}`}>{STATE_WORDS[st.state]}</span> : connBadge(eff(mode), via)} sub={a.sub}>
            <Connection app={name} mode={mode} via={via} detail={f[`${a.slug}_detail`]} core={coreOn} hasToken={!!f[`${a.slug}_token`]}
              onMode={m => save({ [`${a.slug}_mode`]: m } as Partial<SettingsT>)} onDisconnect={() => save({ [`${a.slug}_url`]: '', [`${a.slug}_token`]: '' } as Partial<SettingsT>)}
              test={<><button type="button" className="btn sm" onClick={() => run(a.slug, f[`${a.slug}_url`], f[`${a.slug}_token`])}>Test connection</button>{status(a.slug)}</>}
              direct={<div className="two-col tight">
                <Field label="URL" help="Inside Docker use http://host.docker.internal:PORT or the service name."><input value={f[`${a.slug}_url`]} onChange={e => set(`${a.slug}_url`, e.target.value)} placeholder={`http://host.docker.internal:${a.port}`} /></Field>
                <Field label="API token" help={a.token}><input value={f[`${a.slug}_token`]} onChange={e => set(`${a.slug}_token`, e.target.value)} autoComplete="off" /></Field>
              </div>} />
            {st?.fix && st.state !== 'on' && <p className="muted small"><Icon name="info" size={12} className="inline-ico" /> {st.fix}.</p>}
          </Section>
        )
      })}
      <WhereThingsGo f={f} setF={setF} />
      <Section title="Other Pocket apps" icon="layers" sub="Cameras offers what it sees to the rest of the box through Pocket Core: Pocket Day writes visitors and deliveries into your journal, Pocket Search finds events, Pocket AI can tell you who came today or switch the house to Away, and Pocket Flows can react to a ring at the door. Switched off until you allow them in Pocket Core → Links.">
        <dl className="set-dl">
          <div><dt>cameras:read</dt><dd className="muted">Your cameras and how they are, the mode, and the events with what was seen and their pictures.</dd></div>
          <div><dt>cameras:write</dt><dd className="muted">Switch the mode, mark events seen or kept, note on an event, ring the doorbell or raise an alarm on a camera. Never changes a camera or deletes anything.</dd></div>
          <div><dt>events:read</dt><dd className="muted"><code>cameras.person</code>, <code>vehicle</code>, <code>animal</code>, <code>parcel</code>, <code>doorbell</code>, <code>event</code>, <code>motion</code>, <code>offline</code>, <code>online</code>, <code>mode_changed</code> — for Pocket Flows.</dd></div>
        </dl>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function AITab() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const { data: ai } = useAsync(() => api.get<{ ready: boolean; provider: string; detail: string }>(`${V1}/ai/status`), [])
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const shown = !coreOn && f.ai_provider === 'pocket' ? 'openai' : (f.ai_provider || 'auto')
  return (
    <div className="stack loose">
      <Section title="What the AI does" icon="sparkles" sub="Each event's clearest picture goes to the AI, which says what is there — a person, a vehicle, an animal (one of your pets?), a parcel, or nothing (shadows, rain, a spider on the lens) — in one sentence, and whether it is worth a look. Only real things alert. It knows your pets (Pocket Pets), the parcel due today (Pocket Parcels) and the weather.">
        <SettingRow title="Ask the AI about each event" help="Off: every movement counts (and alerts in the modes a camera watches)."><Switch checked={f.ai_help} onChange={v => save({ ai_help: v })} label="AI" /></SettingRow>
        <SettingRow title="Pictures may go to the AI" help="Pocket AI may send them on to a cloud model, depending on how it is set up. Off: no picture leaves this server."><Switch checked={f.ai_photos} onChange={v => save({ ai_photos: v })} label="Pictures" /></SettingRow>
        {ai && (ai.ready ? <p className="muted small row" style={{ gap: 6 }}><Icon name="sparkles" size={13} />{ai.provider === 'pocket' ? 'Pocket AI' : 'Your OpenAI key'} is ready.</p>
          : <div className="callout info"><Icon name="info" size={15} /><div>No AI connected{ai.detail ? ` (${ai.detail})` : ''}.{!coreOn && <CoreHint />}</div></div>)}
      </Section>
      <Section title="Provider" icon="sparkles">
        <div className="grid" style={{ gap: 12 }}>
          <Field group label="Who writes" help="Automatic prefers your enabled Pocket AI connection; without one it uses your OpenAI settings.">
            <Select ariaLabel="AI provider" value={shown} onChange={v => setF({ ...f, ai_provider: v as SettingsT['ai_provider'] })} options={[{ value: 'auto', label: coreOn ? 'Automatic — prefer Pocket AI' : 'Automatic' }, { value: 'pocket', label: 'Pocket AI', disabled: !coreOn, hint: coreOn ? undefined : 'needs Pocket Core' }, { value: 'openai', label: 'OpenAI / compatible API' }]} />
          </Field>
          {!coreOn && <CoreHint />}
          {coreOn && shown !== 'openai' && <PocketModelSelect value={f.ai_pocket_model || ''} onChange={v => setF({ ...f, ai_pocket_model: v })} />}
          {(shown === 'openai' || (!coreOn && shown === 'auto')) && <>
            <Field label="OpenAI API key" help="platform.openai.com → API keys. Stored on the server; shown masked."><input value={f.openai_api_key} onChange={e => setF({ ...f, openai_api_key: e.target.value })} autoComplete="off" placeholder="sk-…" /></Field>
            <div className="two-col tight">
              <Field label="Model" help="Default gpt-4o-mini."><input value={f.openai_model} onChange={e => setF({ ...f, openai_model: e.target.value })} placeholder="gpt-4o-mini" /></Field>
              <Field label="Base URL (optional)" help="Ollama, LM Studio, OpenRouter…"><input value={f.openai_base_url} onChange={e => setF({ ...f, openai_base_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
            </div>
          </>}
        </div>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {(shown === 'openai' || (!coreOn && shown === 'auto')) && <><button type="button" className="btn" onClick={() => run('openai', f.openai_base_url, f.openai_api_key)} disabled={!f.openai_api_key}>Test the OpenAI key</button>{status('openai')}</>}
      </div>
    </div>
  )
}

function Connection({ app, mode: saved, via, detail, core, onMode, onDisconnect, hasToken, test, direct }: {
  app: string; mode: ConnMode; via: string; detail: string; core: boolean; onMode: (m: ConnMode) => void; onDisconnect: () => void
  hasToken: boolean; test: React.ReactNode; direct: React.ReactNode
}) {
  const [advanced, setAdvanced] = useState(saved === 'direct')
  const mode: ConnMode = !core && saved === 'core' ? 'off' : saved
  const ok = via !== '' && mode !== 'off'
  const showDirect = !core || mode === 'direct' || advanced
  const line = mode === 'off' ? `Off: Cameras does not reach ${app}.`
    : mode === 'core' ? (ok ? `Connected through Pocket Core, as you${detail ? ` (${detail})` : ''}. Nothing to fill in; switch it off for yourself in Pocket Core → Settings → Apps acting for me.` : `Not connected: ${detail}.`)
    : (ok ? `Direct connection with your own ${app} URL and token; use Test connection to check it.` : `Not connected: ${detail || 'add the URL and a token'}.`)
  return (
    <div className="conn-block">
      <div className="conn-mode">
        <Segmented size="sm" value={mode} onChange={m => onMode(m as ConnMode)} options={[
          { value: 'core', label: 'Pocket Core', title: core ? 'Through Pocket Core: per person, nothing to paste' : 'Needs Pocket Core: there is none on this box', disabled: !core },
          ...(showDirect ? [{ value: 'direct', label: 'Direct', title: 'Your own URL and API token' }] : []),
          { value: 'off', label: 'Off' },
        ]} />
        {!showDirect && <button type="button" className="conn-advanced" onClick={() => setAdvanced(true)}>Advanced: connect directly with a URL and key</button>}
      </div>
      <div className={`conn-status ${mode === 'off' ? '' : ok ? 'ok' : 'bad'}`}><span className="dot" />{line}</div>
      {mode === 'direct' && <>
        {direct}
        <div className="row wrap">{test}{hasToken && <button type="button" className="btn sm ghost danger-text" onClick={onDisconnect} title="Remove the saved URL and token">Disconnect</button>}</div>
      </>}
      {mode === 'core' && <div className="row wrap">{test}</div>}
    </div>
  )
}

const connBadge = (mode: ConnMode, via: string) => mode === 'off' ? <span className="badge">off</span>
  : via ? <span className="badge ok">{via === 'core' ? 'Pocket Core' : 'direct'}</span>
  : <span className="badge warn">not connected</span>

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadMeta(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account and has their own cameras, settings and connections. Admins manage accounts; they never see other people's cameras unless shared with them.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account goes, with their cameras, events and recordings." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'User saved' : 'User added'); onSaved() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      setUser(await api.put(`${V1}/auth/me`, f))
      setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}</p></div>
      </div>
      <Section title="You">
        <div className="stack">
          <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="Email" help="The same email as in Pocket Core, so signing in finds your account."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        </div>
      </Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you: it can read and change everything, feed a Homepage widget (<code>GET /api/v1/summary</code>) or add a job from a Siri Shortcut. See <code>docs/api.md</code>.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. iPhone Shortcut)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Endpoints" icon="code" sub={<>At <code>{base}/api/v1</code>.</>}>
        <dl className="set-dl">
          <div><dt><code>POST /cameras/ID/ring</code></dt><dd className="muted">The doorbell was pressed (Home Assistant, a Shelly button): the picture is kept and you are told, in every mode.</dd></div>
          <div><dt><code>POST /cameras/ID/trigger</code></dt><dd className="muted"><code>{'{"note": "Back door opened"}'}</code> — an alarm from a sensor, kept with the picture.</dd></div>
          <div><dt><code>PUT /mode</code></dt><dd className="muted"><code>{'{"mode": "away"}'}</code> (home, away, night, off) — a Shortcut when you leave.</dd></div>
          <div><dt><code>GET /summary</code></dt><dd className="muted">The mode, each camera, today's people, cars and parcels — for Homepage.</dd></div>
          <div><dt><code>GET /cameras/ID/snapshot.jpg</code></dt><dd className="muted">The picture now.</dd></div>
          <div><dt><code>GET /events?label=person&amp;day=YYYY-MM-DD</code></dt><dd className="muted">What was seen.</dd></div>
          <div><dt><code>GET /search?q=…</code></dt><dd className="muted">Cameras and events (“parcel”, “fox”).</dd></div>
          <div><dt><code>GET /day/YYYY-MM-DD</code></dt><dd className="muted">A day, for a journal.</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { user } = useApp()
  const { toast } = useToast()
  const file = React.useRef<HTMLInputElement>(null)
  const bring = async (fl: File) => {
    try {
      const res = await fetch(`${V1}/import`, { method: 'POST', body: await fl.text(), headers: { 'Content-Type': fl.name.endsWith('.json') ? 'application/json' : 'text/yaml' }, credentials: 'same-origin' })
      const j = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(j.error || res.statusText)
      toast(j.text || 'Brought in'); if (j.warnings?.length) toast(j.warnings.join('; '), 'err')
    } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="stack loose">
      <Section title="Export" icon="archive" sub="Everything as one JSON file — your cameras (without passwords), every event with what was seen, and the changes of mode — or a table of every event (CSV). The pictures and recordings themselves are in the data folder (and the box backup).">
        <div className="row wrap"><a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a><a className="btn" href={`${V1}/export.csv`}><Icon name="download" size={15} />Events as CSV</a></div>
      </Section>
      <Section title="Bring a file in" icon="upload" sub="A Pocket Cameras export (also from another box), or a Frigate config.yml: its cameras come over with their streams (go2rtc restreams followed back to the camera), recording, zones, motion masks and the objects it tracked. The same file twice adds nothing twice. Passwords are not in an export: fill them in on each camera.">
        <button type="button" className="btn" onClick={() => file.current?.click()}><Icon name="upload" size={15} />Bring a file…</button>
        <input ref={file} type="file" hidden accept=".json,.yml,.yaml,application/json" onChange={e => { const fl = e.target.files?.[0]; e.target.value = ''; if (fl) bring(fl) }} />
      </Section>
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>). Restore by stopping the app and replacing <code>pocket-cameras.db</code> in the data folder.</p>
        <a className="btn" href={`${V1}/backup`}><Icon name="database" size={15} />Download database</a>
      </Section>}
    </div>
  )
}

function About() {
  const { data, error } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const counts = (data.counts || {}) as Record<string, number>
  const core = (data.core || {}) as { configured?: boolean; registered?: boolean; url?: string; error?: string }
  const tiles: [string, number | undefined, string][] = [['Cameras', counts.cameras, 'camera'], ['Events', counts.events, 'bell'], ['Recorded pieces', counts.segments, 'video'], ['Mode changes', counts.mode_log, 'shield']]
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{String(data.version)}</code>],
    ['Uptime', String(data.uptime)],
    ['Runtime', `${String(data.go)} · ${String(data.os)}/${String(data.arch)}`],
    ['Time zone', String(data.timezone)],
    ['Sign-in', String(data.auth_mode)],
    ['Pocket Core', core.configured ? (core.registered ? <span key="c" className="ok">connected · {core.url}</span> : <span key="c" className="bad">{core.error || 'not registered yet'}</span>) : <span key="c" className="muted">not configured</span>],
    ['ffmpeg', data.ffmpeg ? <span key="ff" className="ok">installed</span> : <span key="ff" className="bad">not installed — RTSP cameras and recording need it</span>],
    ['Recordings', <code key="rd">{String(data.recordings_dir)}</code>],
    ['Database', <span key="db"><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))}</span>],
    ['People', `${counts.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Cameras</h3>
          <p className="muted small">Your security cameras on your own server: live view, movement in the areas you choose, each event looked at by AI, recordings, Home / Away / Night modes that switch by themselves, and alerts on your phone. One Go binary, one SQLite file, one volume.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => (
          <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>
        ))}
      </div>
      <Section title="This server">
        <dl className="set-dl">
          {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
        </dl>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-cameras" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Calendar, Contacts, Family, Email, Chat, Photos, Notes, Files, Voice, Tasks, Alerts, Blog, Weather, Bookshelf, Music, Movement, Health, Wealth, Pocket AI and more.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}

function WhereThingsGo({ f, setF }: { f: SettingsT; setF: (s: SettingsT) => void }) {
  const { data: chats } = useAsync(() => f.chat_via ? api.get<{ conversations: { id: string; name: string }[] }>(`${V1}/apps/chats`).then(r => r.conversations).catch(() => []) : Promise.resolve([]), [f.chat_via])
  const { data: lists } = useAsync(() => f.tasks_via ? api.get<{ lists: { id: string; name: string }[] }>(`${V1}/apps/tasks/lists`).then(r => r.lists).catch(() => []) : Promise.resolve([]), [f.tasks_via])
  const { data: pets } = useAsync(() => f.pets_via ? api.get<{ pets: { Name: string; Species: string }[] }>(`${V1}/apps/pets`).then(r => r.pets).catch(() => []) : Promise.resolve([]), [f.pets_via])
  return (
    <Section title="How they help" icon="layers">
      <SettingRow title="Your own pets do not alert" help={f.pets_via ? (pets?.length ? `The AI knows ${pets.map(p => p.Name).join(', ')}.` : 'No pets in Pocket Pets yet.') : 'Needs Pocket Pets.'}><Switch checked={f.pets_quiet} disabled={!f.pets_via && !f.pets_quiet} onChange={v => setF({ ...f, pets_quiet: v })} label="Pets" /></SettingRow>
      <SettingRow title="In a storm, movement alone does not alert" help={f.weather_via ? 'Strong wind, heavy rain or snow (Pocket Weather) — when the AI cannot look.' : 'Needs Pocket Weather.'}><Switch checked={f.weather_calm} disabled={!f.weather_via && !f.weather_calm} onChange={v => setF({ ...f, weather_calm: v })} label="Weather" /></SettingRow>
      <SettingRow title="A camera offline for a day goes into its Pocket Home log" help={f.home_via ? 'For cameras linked to a thing in Pocket Home (on the camera\'s page).' : 'Needs Pocket Home.'}><Switch checked={f.home_log} disabled={!f.home_via && !f.home_log} onChange={v => setF({ ...f, home_log: v })} label="Home log" /></SettingRow>
      <SettingRow title="Shared events go to" help="The Pocket Chat conversation; empty: a note to yourself.">
        {chats?.length ? <Select ariaLabel="Chat" value={f.chat_to} onChange={v => setF({ ...f, chat_to: v })} placeholder="Me" clearLabel="Me" options={chats.map(c => ({ value: c.id, label: c.name || c.id }))} /> : <input value={f.chat_to} onChange={e => setF({ ...f, chat_to: e.target.value })} placeholder="me" disabled={!f.chat_via} />}
      </SettingRow>
      <SettingRow title="Tasks go to">
        <Select ariaLabel="List" value={f.tasks_list} onChange={v => setF({ ...f, tasks_list: v })} placeholder="The inbox" clearLabel="The inbox" disabled={!f.tasks_via} options={(lists || []).map(l => ({ value: l.id, label: l.name }))} />
      </SettingRow>
      <SettingRow title="Notes folder" help="Where events go in Pocket Notes."><input value={f.notes_folder} onChange={e => setF({ ...f, notes_folder: e.target.value })} disabled={!f.notes_via} /></SettingRow>
      <SettingRow title="Files folder" help="Where clips and pictures go in Pocket Files."><input value={f.files_folder} onChange={e => setF({ ...f, files_folder: e.target.value })} disabled={!f.files_via} /></SettingRow>
    </Section>
  )
}
