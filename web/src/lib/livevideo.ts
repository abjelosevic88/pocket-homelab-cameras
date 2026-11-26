// Plays a camera's live video (fragmented MP4 from /live.mp4) in a <video>
// through Media Source Extensions: the camera's own pictures at its own
// rate, kept close to "now" by jumping ahead whenever the browser falls behind.

type MSCtor = typeof MediaSource

/** The Media Source the browser has (iPhone: ManagedMediaSource), or null. */
export function mediaSource(): MSCtor | null {
  const w = window as unknown as { ManagedMediaSource?: MSCtor; MediaSource?: MSCtor }
  return w.ManagedMediaSource || w.MediaSource || null
}

const hex2 = (n: number) => n.toString(16).padStart(2, '0').toUpperCase()

function findBox(b: Uint8Array, type: string, from = 0): number {
  const t = [...type].map(c => c.charCodeAt(0))
  for (let i = from; i + 4 <= b.length; i++) {
    if (b[i] === t[0] && b[i + 1] === t[1] && b[i + 2] === t[2] && b[i + 3] === t[3]) return i
  }
  return -1
}

function reverseBits32(x: number): number {
  let r = 0
  for (let i = 0; i < 32; i++) { r = (r << 1) | (x & 1); x >>>= 1 }
  return r >>> 0
}

/** The codec string (RFC 6381) from an MP4 init segment's avcC / hvcC box. */
export function codecFromInit(b: Uint8Array): string | null {
  const avc = findBox(b, 'avcC')
  if (avc >= 0 && avc + 8 <= b.length) {
    const p = avc + 4 // configurationVersion, profile, compatibility, level
    return `avc1.${hex2(b[p + 1])}${hex2(b[p + 2])}${hex2(b[p + 3])}`
  }
  const hvc = findBox(b, 'hvcC')
  if (hvc >= 0 && hvc + 4 + 13 <= b.length) {
    const p = hvc + 4
    const space = b[p + 1] >> 6, tier = (b[p + 1] >> 5) & 1, profile = b[p + 1] & 0x1f
    const compat = ((b[p + 2] << 24) | (b[p + 3] << 16) | (b[p + 4] << 8) | b[p + 5]) >>> 0
    const level = b[p + 12]
    const cons = Array.from(b.slice(p + 6, p + 12))
    while (cons.length && cons[cons.length - 1] === 0) cons.pop()
    const entry = findBox(b, 'hev1') >= 0 ? 'hev1' : 'hvc1'
    return `${entry}.${['', 'A', 'B', 'C'][space]}${profile}.${reverseBits32(compat).toString(16).toUpperCase()}.${tier ? 'H' : 'L'}${level}${cons.map(c => '.' + hex2(c)).join('')}`
  }
  return null
}

/** Where the init segment (ftyp + moov) ends, or -1 while it is incomplete. */
export function initEnd(b: Uint8Array): number {
  let i = 0
  while (i + 8 <= b.length) {
    const size = ((b[i] << 24) | (b[i + 1] << 16) | (b[i + 2] << 8) | b[i + 3]) >>> 0
    const type = String.fromCharCode(b[i + 4], b[i + 5], b[i + 6], b[i + 7])
    if (size < 8) return -1
    if (i + size > b.length) return -1
    i += size
    if (type === 'moov') return i
  }
  return -1
}

function concat(chunks: Uint8Array[]): Uint8Array {
  if (chunks.length === 1) return chunks[0]
  let n = 0
  for (const c of chunks) n += c.length
  const out = new Uint8Array(n)
  let o = 0
  for (const c of chunks) { out.set(c, o); o += c.length }
  return out
}

/** How far behind the newest picture the video plays (seconds), and when it jumps ahead. */
const LIVE_TARGET = 0.08, LIVE_JUMP = 0.5

export type LiveState = 'playing' | 'failed' | 'ended'

/**
 * Plays url in video until the returned stop is called. onState: 'playing'
 * once the first picture shows; 'failed' when it never could (the browser
 * cannot play this camera's video: show pictures instead); 'ended' when a
 * stream that played stopped (try again).
 */
export function playLive(video: HTMLVideoElement, url: string, onState: (s: LiveState) => void): () => void {
  const MS = mediaSource()
  if (!MS) { onState('failed'); return () => {} }
  const ac = new AbortController()
  const ms = new MS()
  let stopped = false, played = false, sb: SourceBuffer | null = null
  const queue: Uint8Array[] = []
  const finish = (s: LiveState) => {
    if (stopped) return
    stopped = true
    ac.abort()
    window.clearTimeout(watchdog)
    window.clearInterval(ticker)
    onState(s === 'failed' && played ? 'ended' : s)
  }
  // no picture after this long (a camera with a long key-frame gap, or video the browser will not decode)
  const watchdog = window.setTimeout(() => { if (!played) finish('failed') }, 15000)

  // Stay as close to the newest picture as the stream allows: a jump when far
  // behind (ffmpeg's first second arrives in one go, and a network hiccup
  // piles pictures up), a slightly faster play when a little behind.
  let jumped = false
  const keepLive = () => {
    const b = video.buffered
    if (!b.length || stopped) return
    const start = b.start(b.length - 1), end = b.end(b.length - 1)
    const behind = end - video.currentTime
    if (video.currentTime < start || behind > LIVE_JUMP || (!jumped && behind > LIVE_TARGET * 2)) {
      video.currentTime = Math.max(start, end - LIVE_TARGET)
      jumped = true
    } else if (behind > LIVE_TARGET * 2.5) video.playbackRate = 1.15
    else if (behind < LIVE_TARGET * 1.2) video.playbackRate = 1
    if (video.paused) video.play().catch(() => {})
  }
  const ticker = window.setInterval(keepLive, 200)
  const pump = () => {
    if (!sb || sb.updating || stopped) return
    const b = video.buffered
    if (b.length && video.currentTime - b.start(0) > 30) {
      try { sb.remove(b.start(0), video.currentTime - 10); return } catch { /* append instead */ }
    }
    if (!queue.length) return
    const chunk = concat(queue.splice(0))
    try { sb.appendBuffer(chunk as BufferSource) } catch { finish('failed') }
  }
  const onPlaying = () => { if (!played && !stopped) { played = true; window.clearTimeout(watchdog); onState('playing') } }
  const onError = () => finish('failed')
  video.addEventListener('playing', onPlaying)
  video.addEventListener('error', onError)
  video.disableRemotePlayback = true // ManagedMediaSource wants it
  const objURL = URL.createObjectURL(ms)
  video.src = objURL

  ms.addEventListener('sourceopen', async () => {
    try {
      const res = await fetch(url, { signal: ac.signal, credentials: 'same-origin' })
      if (!res.ok || !res.body) { finish('failed'); return }
      const reader = res.body.getReader()
      let head: Uint8Array[] = []
      for (;;) {
        const { value, done } = await reader.read()
        if (done || stopped) break
        if (!value?.length) continue
        if (!sb) {
          head.push(value)
          const all = concat(head)
          head = [all]
          const end = initEnd(all)
          if (end < 0) { if (all.length > 4 << 20) { finish('failed'); return } continue }
          const codec = codecFromInit(all.subarray(0, end))
          const type = `video/mp4; codecs="${codec}"`
          if (!codec || !MS.isTypeSupported(type)) { finish('failed'); return }
          sb = ms.addSourceBuffer(type)
          sb.addEventListener('updateend', () => { keepLive(); pump() })
          queue.push(all)
        } else {
          queue.push(value)
          if (queue.length > 600) { finish('ended'); return } // the browser stopped taking video
        }
        pump()
      }
      finish(played ? 'ended' : 'failed')
    } catch {
      finish(played ? 'ended' : 'failed')
    }
  }, { once: true })

  return () => {
    stopped = true
    ac.abort()
    window.clearTimeout(watchdog)
    window.clearInterval(ticker)
    video.removeEventListener('playing', onPlaying)
    video.removeEventListener('error', onError)
    video.removeAttribute('src')
    video.load()
    URL.revokeObjectURL(objURL)
  }
}
