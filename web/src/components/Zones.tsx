// Draw the parts of a camera's picture to watch, or to ignore (a busy street, a tree).
import { useCallback, useRef, useState } from 'react'
import type { Zone } from '../lib/types'
import { snapshotURL } from '../lib/cams'
import { Icon } from './icons'
import { Segmented } from './ui'

function useBox() {
  const [size, setSize] = useState({ w: 0, h: 0 })
  const ro = useRef<ResizeObserver | null>(null)
  const ref = useCallback((el: HTMLDivElement | null) => {
    ro.current?.disconnect(); ro.current = null
    if (!el) return
    const measure = () => { const r = el.getBoundingClientRect(); if (r.width > 0) setSize({ w: Math.round(r.width), h: Math.round(r.height) }) }
    measure(); ro.current = new ResizeObserver(measure); ro.current.observe(el)
  }, [])
  return [ref, size] as const
}

export function ZonesEditor({ cameraId, zones, onChange }: { cameraId: number; zones: Zone[]; onChange: (z: Zone[]) => void }) {
  const [ref, { w, h }] = useBox()
  const [drawing, setDrawing] = useState<[number, number][] | null>(null)
  const [kind, setKind] = useState<'watch' | 'ignore'>('watch')
  const [drag, setDrag] = useState<{ z: number; p: number } | null>(null)
  const svg = useRef<SVGSVGElement>(null)
  const [pic] = useState(() => snapshotURL(cameraId, Date.now()))

  const at = (e: React.PointerEvent): [number, number] => {
    const r = svg.current!.getBoundingClientRect()
    return [Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), Math.min(1, Math.max(0, (e.clientY - r.top) / r.height))]
  }
  const finish = () => {
    if (drawing && drawing.length >= 3) {
      const n = zones.filter(z => z.kind === kind).length + 1
      onChange([...zones, { name: kind === 'watch' ? `Watch ${n}` : `Ignore ${n}`, kind, points: drawing }])
    }
    setDrawing(null)
  }
  const down = (e: React.PointerEvent) => {
    if (!drawing) return
    const p = at(e)
    if (drawing.length >= 3) {
      const [x0, y0] = drawing[0]
      if (Math.hypot((p[0] - x0) * w, (p[1] - y0) * h) < 14) { finish(); return }
    }
    setDrawing([...drawing, p])
  }
  const move = (e: React.PointerEvent) => {
    if (!drag) return
    const p = at(e)
    onChange(zones.map((z, i) => i !== drag.z ? z : { ...z, points: z.points.map((q, j) => j === drag.p ? p : q) }))
  }
  const px = (p: [number, number]) => `${(p[0] * w).toFixed(1)},${(p[1] * h).toFixed(1)}`

  return (
    <div className="zones">
      <div className="zones-pic" ref={ref}>
        <img src={pic} alt="" draggable={false} />
        {w > 0 && <svg ref={svg} width={w} height={h} viewBox={`0 0 ${w} ${h}`} className={drawing ? 'drawing' : ''}
          onPointerDown={down} onPointerMove={move} onPointerUp={() => setDrag(null)} onPointerLeave={() => setDrag(null)}>
          {zones.map((z, i) => (
            <g key={i} className={`zone zone-${z.kind}`}>
              <polygon points={z.points.map(px).join(' ')} />
              {!drawing && z.points.map((p, j) => <circle key={j} cx={p[0] * w} cy={p[1] * h} r={6} onPointerDown={e => { e.stopPropagation(); (e.target as Element).setPointerCapture?.(e.pointerId); setDrag({ z: i, p: j }) }} />)}
            </g>
          ))}
          {drawing && <g className={`zone zone-${kind} zone-new`}>
            <polyline points={drawing.map(px).join(' ')} />
            {drawing.map((p, j) => <circle key={j} cx={p[0] * w} cy={p[1] * h} r={j === 0 ? 8 : 5} />)}
          </g>}
        </svg>}
      </div>
      <div className="zones-side">
        {drawing ? <>
          <p className="small">Tap the corners of the area on the picture; tap the first corner (or Done) to close it.</p>
          <div className="row wrap">
            <button type="button" className="btn primary sm" disabled={drawing.length < 3} onClick={finish}><Icon name="check" size={14} />Done</button>
            <button type="button" className="btn sm" disabled={!drawing.length} onClick={() => setDrawing(drawing.slice(0, -1))}><Icon name="undo" size={14} />Undo a corner</button>
            <button type="button" className="btn ghost sm" onClick={() => setDrawing(null)}>Cancel</button>
          </div>
        </> : <>
          <Segmented size="sm" value={kind} onChange={v => setKind(v as 'watch' | 'ignore')} options={[{ value: 'watch', label: 'Watch' }, { value: 'ignore', label: 'Ignore' }]} />
          <p className="muted small">{kind === 'watch' ? 'Only movement inside “watch” areas counts (with none, the whole picture does).' : 'Movement inside “ignore” areas never counts: the street, a tree, a flag, the neighbour\'s window.'}</p>
          <button type="button" className="btn sm" onClick={() => setDrawing([])}><Icon name="plus" size={14} />Draw an area to {kind}</button>
        </>}
        <div className="zones-list">
          {zones.map((z, i) => (
            <div key={i} className="zones-row">
              <span className={`zone-swatch zone-${z.kind}`} />
              <input value={z.name} onChange={e => onChange(zones.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} aria-label="Area name" />
              <button type="button" className="btn ghost sm" title={z.kind === 'watch' ? 'Make it an area to ignore' : 'Make it an area to watch'} onClick={() => onChange(zones.map((x, j) => j === i ? { ...x, kind: x.kind === 'watch' ? 'ignore' : 'watch' } : x))}>{z.kind === 'watch' ? 'Watch' : 'Ignore'}</button>
              <button type="button" className="btn ghost icon-only sm" aria-label="Remove" title="Remove" onClick={() => onChange(zones.filter((_, j) => j !== i))}><Icon name="trash" size={14} /></button>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
