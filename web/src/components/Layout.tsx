// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { DataProvider } from '../lib/data'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { Avatar, type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'
import { CameraDialog } from './dialogs'

export { announceChange, useOnChange } from '../lib/data'

const SIDEBAR_KEY = 'pcam-sidebar'

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string, to?: string) => void>(() => {})
export function useCrumb(title: string | undefined, parent?: string) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || '', parent); return () => set('') }, [title, parent, set])
}

/** Add a camera from anywhere (a page's button). */
export const openCreate = (template?: string) => window.dispatchEvent(new CustomEvent('pcam:create', { detail: { template } }))

export default function Layout() {
  return <DataProvider><Shell /></DataProvider>
}

function Shell() {
  const navigate = useNavigate()
  const loc = useLocation()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState<{ t: string; to?: string }>({ t: '' })
  const setCrumb = useCallback((t: string, to?: string) => setCrumbRaw({ t, to }), [])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)

  const [starting, setStarting] = useState(false)
  const create = () => {
    closeDrawer()
    setStarting(true)
  }
  const createRef = useRef(create)
  createRef.current = create

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = () => createRef.current()
    window.addEventListener('pcam:shortcuts', open)
    window.addEventListener('pcam:create', make)
    return () => { window.removeEventListener('pcam:shortcuts', open); window.removeEventListener('pcam:create', make) }
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': e.preventDefault(); createRef.current(); break
        case 'h': go('/'); break
        case 'e': go('/events'); break
        case 't': go('/timeline'); break
        case 'c': go('/cameras'); break
        case 's': go('/stats'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const setMode = (mode: string) => api.put(`${V1}/mode`, { mode }).then(() => window.dispatchEvent(new CustomEvent('pcam:changed'))).catch(() => {})
  const commands: PaletteCommand[] = [
    { id: 'add', label: 'Add a camera', icon: 'plus', hint: 'N', keywords: 'new rtsp reolink hikvision tapo', run: () => create() },
    { id: 'find', label: 'Find cameras on the network', icon: 'scan', keywords: 'discover scan search onvif', run: () => navigate('/cameras?find=1') },
    { id: 'live', label: 'Live', icon: 'grid', hint: 'H', keywords: 'home cameras now watch', run: () => navigate('/') },
    { id: 'events', label: 'Events', icon: 'bell', hint: 'E', keywords: 'motion people parcels alerts', run: () => navigate('/events') },
    { id: 'unseen', label: 'Events not seen yet', icon: 'eye', keywords: 'new unreviewed', run: () => navigate('/events?unreviewed=1') },
    { id: 'starred', label: 'Kept events', icon: 'star', keywords: 'starred saved', run: () => navigate('/events?starred=1') },
    { id: 'timeline', label: 'Timeline', icon: 'video', hint: 'T', keywords: 'recordings playback day', run: () => navigate('/timeline') },
    { id: 'cameras', label: 'Cameras', icon: 'camera', hint: 'C', run: () => navigate('/cameras') },
    { id: 'stats', label: 'Numbers', icon: 'chart', hint: 'S', keywords: 'stats storage', run: () => navigate('/stats') },
    { id: 'mode-home', label: 'Mode: Home', icon: 'home', keywords: 'arm disarm', run: () => setMode('home') },
    { id: 'mode-away', label: 'Mode: Away', icon: 'plane', keywords: 'arm', run: () => setMode('away') },
    { id: 'mode-night', label: 'Mode: Night', icon: 'moon', keywords: 'arm sleep', run: () => setMode('night') },
    { id: 'mode-off', label: 'Mode: Off (pause alerts)', icon: 'pause', keywords: 'disarm pause', run: () => setMode('off') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'modes', label: 'Settings: modes', icon: 'shield', keywords: 'auto away night travel phones', run: () => navigate('/settings/general') },
    { id: 'apps', label: 'Settings: connected apps', icon: 'link', keywords: 'alerts network movement travel parcels pets weather chat tasks notes photos files rooms home core', run: () => navigate('/settings/apps') },
    { id: 'notif', label: 'Settings: notifications', icon: 'bell', keywords: 'push phone', run: () => navigate('/settings/notifications') },
    { id: 'ai-set', label: 'Settings: AI', icon: 'sparkles', keywords: 'pocket ai openai vision', run: () => navigate('/settings/ai') },
    { id: 'export', label: 'Settings: export and import (Frigate too)', icon: 'download', keywords: 'backup json csv frigate', run: () => navigate('/settings/data') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onCreate={create} onNavigate={closeDrawer} />
        </div>
        <div className="main">
          <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
          <main className="content"><div className="content-inner"><Outlet /></div></main>
        </div>
        {mobile && (loc.pathname === '/' || loc.pathname === '/cameras') && <button type="button" className="fab" aria-label="Add a camera" title="Add a camera" onClick={() => create()}><Icon name="plus" size={26} strokeWidth={2} /></button>}
        {starting && <CameraDialog onClose={() => setStarting(false)} onSaved={c => { window.dispatchEvent(new CustomEvent('pcam:changed')); navigate(`/cameras/${c.id}`) }} />}
        {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
      </div>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = {
  '': ['Live', '/'], events: ['Events', '/events'], timeline: ['Timeline', '/timeline'], cameras: ['Cameras', '/cameras'], stats: ['Numbers', '/stats'],
}

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: { t: string; to?: string } }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pcam:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb.t ? <><button type="button" className="crumb" onClick={() => navigate(crumb.to || sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb.t}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
