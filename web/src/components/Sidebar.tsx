// Left column: add a camera, the mode, the sections, and each camera now.
import { useCallback, useEffect, useState } from 'react'
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useOnChange } from '../lib/data'
import { MODE } from '../lib/cams'
import type { Home } from '../lib/types'
import { Icon } from './icons'

export function Sidebar({ onCreate, onNavigate }: { onCreate: () => void; onNavigate: () => void }) {
  const { status, settings } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [home, setHome] = useState<Home | null>(null)
  const load = useCallback(() => { api.get<Home>(`${V1}/home`).then(setHome).catch(() => {}) }, [])
  useEffect(() => { load() }, [load, loc.pathname])
  useOnChange(load)
  useEffect(() => {
    const t = window.setInterval(() => { if (document.visibilityState === 'visible') load() }, 15000)
    return () => window.clearInterval(t)
  }, [load])

  const title = settings?.title || status?.title || 'Pocket Cameras'
  const path = loc.pathname
  const offline = home ? home.cameras.filter(c => c.enabled && !c.online).length : 0
  const nav: { to: string; label: string; icon: string; active: boolean; count?: number | string; strong?: boolean; hint?: string }[] = [
    { to: '/', label: 'Live', icon: 'grid', active: path === '/', count: home?.total ? `${home.online}/${home.total}` : undefined, strong: offline > 0, hint: offline ? `${offline} offline` : undefined },
    { to: '/events', label: 'Events', icon: 'bell', active: path.startsWith('/events'), count: home?.unreviewed || undefined, strong: !!home?.unreviewed, hint: home?.unreviewed ? 'Not seen yet' : undefined },
    { to: '/timeline', label: 'Timeline', icon: 'video', active: path.startsWith('/timeline') },
    { to: '/cameras', label: 'Cameras', icon: 'camera', active: path === '/cameras' },
    { to: '/stats', label: 'Numbers', icon: 'chart', active: path.startsWith('/stats') },
  ]
  const mode = home ? MODE[home.mode] : null
  return (
    <aside className="sidebar" aria-label="Pocket Cameras">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Cameras' ? title : <>Pocket <b>Cameras</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" onClick={onCreate} title="Add a camera (N)">
        <Icon name="plus" size={15} strokeWidth={2.2} />Add a camera
      </button>

      {mode && <button type="button" className={`sb-mode mode-${home!.mode}`} onClick={() => { onNavigate(); navigate('/?mode=1') }} title="Change the mode">
        <Icon name={mode.icon} size={15} /><span className="grow">{mode.label}</span>{home!.auto_mode && <span className="muted small">auto</span>}
      </button>}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(x => (
          <NavLink key={x.to} to={x.to} onClick={onNavigate} title={x.hint} className={() => `sb-nav-item ${x.active ? 'active' : ''}`}>
            <Icon name={x.icon} size={16} />{x.label}
            {!!x.count && <span className={`sb-count ${x.strong ? 'strong' : ''}`}>{x.count}</span>}
          </NavLink>
        ))}
      </nav>

      {!!home?.cameras.length && <section className="sb-sec">
        <h4 className="sb-h">Cameras</h4>
        {home.cameras.map(c => (
          <button key={c.id} type="button" className={`sb-row sb-list ${path === `/cameras/${c.id}` ? 'active' : ''}`} onClick={() => { onNavigate(); navigate(`/cameras/${c.id}`) }}
            title={`${c.name}: ${!c.enabled ? 'switched off' : c.online ? (c.event ? 'something is moving' : 'online') : 'offline'}`}>
            <span className={`sb-state ${!c.enabled ? 'tone-off' : c.online ? (c.event ? 'tone-move' : 'tone-ok') : 'tone-bad'}`} />
            <span className="grow ellipsis">{c.name}</span>
            {!c.mine && <Icon name="users" size={12} className="muted" />}
          </button>
        ))}
      </section>}

      <div className="sb-foot">
        <NavLink to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</NavLink>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pcam:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
