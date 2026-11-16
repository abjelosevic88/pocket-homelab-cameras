// The other Pocket apps Cameras works with.
import type { AppSlug, AppStateName } from './types'

export const APP_INFO: Record<AppSlug, { name: string; icon: string }> = {
  alerts: { name: 'Pocket Alerts', icon: 'bell' }, network: { name: 'Pocket Network', icon: 'server' }, movement: { name: 'Pocket Movement', icon: 'navigation' },
  travel: { name: 'Pocket Travel', icon: 'plane' }, parcels: { name: 'Pocket Parcels', icon: 'package' }, pets: { name: 'Pocket Pets', icon: 'paw' },
  weather: { name: 'Pocket Weather', icon: 'cloud-rain' }, chat: { name: 'Pocket Chat', icon: 'message' }, tasks: { name: 'Pocket Tasks', icon: 'check-square' },
  notes: { name: 'Pocket Notes', icon: 'note' }, photos: { name: 'Pocket Photos', icon: 'image' }, files: { name: 'Pocket Files', icon: 'archive' },
  rooms: { name: 'Pocket Rooms', icon: 'layout' }, home: { name: 'Pocket Home', icon: 'home' },
}

export const STATE_WORDS: Record<AppStateName, string> = {
  on: 'Connected', partly: 'Partly connected', off: 'Off', link_off: 'Link is off in Pocket Core', missing: 'Not on this box', old: 'Needs an update',
  no_core: 'No Pocket Core', not_set_up: 'Not set up', error: 'Not working',
}
