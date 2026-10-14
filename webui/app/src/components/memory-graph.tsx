// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import type { MemoryGraph, MemoryGraphEdge, MemoryGraphNode } from '../api/client'
import { t } from '../i18n'

/** The memory knowledge graph (v0.0.10): every memory surface and skill as a
 *  node; edges are provenance ([from:…] promotions) and literal name mentions,
 *  weighted by their evidence count. Rendering is dependency-free SVG — a
 *  small deterministic force relaxation gives the organic layout a canvas
 *  library would, without the bundle cost of shipping one inside the Go
 *  binary. Wheel zooms, dragging the background pans. */

interface Placed extends MemoryGraphNode {
  x: number
  y: number
}

interface View {
  k: number // zoom scale
  x: number // pan offset, graph units
  y: number
}

const VIEW_HOME: View = { k: 1, x: 0, y: 0 }
const ZOOM_MIN = 0.4
const ZOOM_MAX = 5

/** Kind → lane on the X axis. The graph reads left-to-right the way memory
 *  flows: raw diary on the left, long-term surfaces in the middle, topical
 *  and project files fanned out on the right, skills below the fold. */
const LANE_X: Record<string, number> = {
  daily: 0.1,
  core: 0.34,
  topic: 0.62,
  project: 0.62,
  skill: 0.88,
}
const LANE_JITTER: Record<string, number> = {
  topic: 0.1,
  project: -0.08,
}

function layout(graph: MemoryGraph, w: number, h: number): Map<string, Placed> {
  const nodes = graph.nodes
  const byKind = new Map<string, MemoryGraphNode[]>()
  for (const n of nodes) {
    const list = byKind.get(n.kind) ?? []
    list.push(n)
    byKind.set(n.kind, list)
  }
  const pos = new Map<string, Placed>()
  // Deterministic seed: nodes fan out inside their lane vertically, in label
  // order — the same data always lands in the same picture.
  for (const [kind, list] of byKind) {
    const cx = (LANE_X[kind] ?? 0.5) * w + (LANE_JITTER[kind] ?? 0) * w
    const sorted = [...list].sort((a, b) => a.label.localeCompare(b.label))
    sorted.forEach((n, i) => {
      const frac = (i + 1) / (sorted.length + 1)
      pos.set(n.id, {
        ...n,
        x: cx,
        y: h * (0.08 + 0.84 * frac),
      })
    })
  }
  // Relax: springs along edges + gentle mutual repulsion. ~90 iterations is
  // enough to unfold crossings without jitter on tiny graphs.
  const edges = graph.edges
  const K = Math.min(w, h) / 4
  for (let it = 0; it < 90; it++) {
    const delta = new Map<string, { dx: number; dy: number }>()
    const push = (id: string, dx: number, dy: number) => {
      const d = delta.get(id) ?? { dx: 0, dy: 0 }
      d.dx += dx
      d.dy += dy
      delta.set(id, d)
    }
    for (const e of edges) {
      const a = pos.get(e.from)
      const b = pos.get(e.to)
      if (!a || !b) continue
      const dx = b.x - a.x
      const dy = b.y - a.y
      const dist = Math.max(Math.hypot(dx, dy), 1)
      const pull = (dist - K) * 0.02
      push(a.id, (dx / dist) * pull, (dy / dist) * pull)
      push(b.id, (-dx / dist) * pull, (-dy / dist) * pull)
    }
    const list = nodes.map((n) => pos.get(n.id)!)
    for (let i = 0; i < list.length; i++) {
      for (let j = i + 1; j < list.length; j++) {
        const a = list[i]
        const b = list[j]
        if (!a || !b) continue
        let dx = b.x - a.x
        let dy = b.y - a.y
        const d2 = dx * dx + dy * dy
        if (d2 < 1) {
          dx = 0.5
          dy = 0.5
        }
        const dist = Math.max(Math.sqrt(d2), 12)
        const rep = Math.min((K * K) / (dist * dist), 6) * 0.6
        push(a.id, (-dx / dist) * rep, (-dy / dist) * rep)
        push(b.id, (dx / dist) * rep, (dy / dist) * rep)
      }
    }
    for (const n of nodes) {
      const p = pos.get(n.id)!
      const d = delta.get(n.id)
      if (d) {
        p.x += d.dx
        p.y += d.dy
      }
      // Lane pull keeps the left-to-right story readable instead of letting
      // the sim collapse kinds into a knot.
      const laneX = (LANE_X[n.kind] ?? 0.5) * w + (LANE_JITTER[n.kind] ?? 0) * w
      p.x += (laneX - p.x) * 0.012
      p.x = Math.min(w - 40, Math.max(40, p.x))
      p.y = Math.min(h - 30, Math.max(30, p.y))
    }
  }
  return pos
}

function nodeRadius(n: MemoryGraphNode): number {
  if (n.kind === 'core') return 16
  const scale = Math.sqrt(Math.max(n.chars ?? 0, 1))
  return Math.min(14, Math.max(6, scale * 0.9))
}

/** Edge thickness follows evidence: a repeated mention or several promotions
 *  draw a heavier line than a stray one. */
function edgeWidth(e: MemoryGraphEdge): number {
  const base = e.kind === 'promotion' ? 1.7 : 1.2
  const bonus = (Math.sqrt(e.weight ?? 1) - 1) * 0.8
  return Math.min(6, base + bonus)
}

const KIND_CLASS: Record<string, string> = {
  core: 'mg-core',
  topic: 'mg-topic',
  project: 'mg-project',
  daily: 'mg-daily',
  skill: 'mg-skill',
}

/** Kind → (strong, soft) theme vars feeding the node's radial gradient — a
 *  pale core with a tinted rim reads as a sphere instead of a flat disc. */
const KIND_FILL: Record<string, [strong: string, soft: string]> = {
  core: ['accent', 'accent-soft'],
  topic: ['accent-2', 'accent-2-soft'],
  project: ['semantic-info', 'semantic-info-soft'],
  daily: ['semantic-warn', 'semantic-warn-soft'],
  skill: ['danger', 'danger-soft'],
}

export function MemoryGraphView({
  graph,
  onOpen,
}: {
  graph: MemoryGraph
  onOpen?(node: MemoryGraphNode): void
}) {
  const wrap = useRef<HTMLDivElement>(null)
  const svgRef = useRef<SVGSVGElement>(null)
  const [size, setSize] = useState({ w: 860, h: 520 })
  const [hover, setHover] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [view, setView] = useState<View>(VIEW_HOME)
  const [panning, setPanning] = useState(false)
  const drag = useRef<{ px: number; py: number; vx: number; vy: number } | null>(null)

  useEffect(() => {
    const el = wrap.current
    if (!el) return
    const ro = new ResizeObserver(() => {
      const r = el.getBoundingClientRect()
      if (r.width > 0) setSize({ w: r.width, h: Math.max(420, Math.min(640, r.width * 0.62)) })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const zoomAt = (cx: number, cy: number, factor: number) => {
    setView((v) => {
      const k = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, v.k * factor))
      const s = k / v.k
      return { k, x: cx - (cx - v.x) * s, y: cy - (cy - v.y) * s }
    })
  }

  // Manual wheel binding: React-style synthetic handlers cannot preventDefault
  // inside a passive listener, so the zoom lives on a real non-passive one.
  useEffect(() => {
    const el = svgRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      const s = size.w / r.width
      const cx = (e.clientX - r.left) * s
      const cy = (e.clientY - r.top) * s
      zoomAt(cx, cy, e.deltaY < 0 ? 1.15 : 1 / 1.15)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [size.w])

  const pos = useMemo(() => layout(graph, size.w, size.h), [graph, size.w, size.h])

  // Parallel edges between the same pair fan out: each gets an alternating,
  // growing perpendicular offset so a promotion and a reference between the
  // same surfaces never draw on top of each other.
  const edgeSpread = useMemo(() => {
    const seen = new Map<string, number>()
    const out = new Map<number, number>()
    graph.edges.forEach((e, i) => {
      const pair = [e.from, e.to].sort().join('|')
      const n = seen.get(pair) ?? 0
      seen.set(pair, n + 1)
      out.set(i, (n % 2 === 0 ? 1 : -1) * (16 + Math.floor(n / 2) * 26))
    })
    return out
  }, [graph])

  const neighbors = useMemo(() => {
    const set = new Set<string>()
    if (hover) {
      for (const e of graph.edges) {
        if (e.from === hover) set.add(e.to)
        if (e.to === hover) set.add(e.from)
      }
    }
    return set
  }, [graph, hover])

  const sel = selected ? graph.nodes.find((n) => n.id === selected) : null
  const selEdges = useMemo(
    () => (sel ? graph.edges.filter((e) => e.from === sel.id || e.to === sel.id) : []),
    [graph, sel],
  )
  const labelOf = (id: string) => graph.nodes.find((n) => n.id === id)?.label ?? id

  const viewDirty = Math.abs(view.k - 1) > 0.01 || Math.abs(view.x) > 1 || Math.abs(view.y) > 1

  return (
    <div class={`mg-wrap${panning ? ' panning' : ''}`} ref={wrap}>
      <svg
        ref={svgRef}
        class="mg-canvas"
        viewBox={`0 0 ${size.w} ${size.h}`}
        role="img"
        aria-label={t('memory.graphTitle')}
        onPointerDown={(e) => {
          const el = e.target as Element
          if (!el.classList?.contains('mg-bg')) return
          el.setPointerCapture?.(e.pointerId)
          drag.current = { px: e.clientX, py: e.clientY, vx: view.x, vy: view.y }
          setPanning(true)
        }}
        onPointerMove={(e) => {
          const d = drag.current
          if (!d) return
          const r = svgRef.current!.getBoundingClientRect()
          const s = size.w / r.width
          setView((v) => ({
            ...v,
            x: d.vx + (e.clientX - d.px) * s,
            y: d.vy + (e.clientY - d.py) * s,
          }))
        }}
        onPointerUp={() => {
          drag.current = null
          setPanning(false)
        }}
        onPointerCancel={() => {
          drag.current = null
          setPanning(false)
        }}
      >
        <defs>
          {Object.entries(KIND_FILL).map(([kind, [strong, soft]]) => {
            return (
              <radialGradient key={kind} id={`mg-fill-${kind}`} cx="38%" cy="30%" r="95%">
                <stop offset="0%" style={{ stopColor: `var(--${soft})` }} />
                <stop offset="100%" style={{ stopColor: `var(--${strong})`, stopOpacity: 0.3 }} />
              </radialGradient>
            )
          })}
          {/* Viewport-fixed dot grid — the canvas texture, deliberately not
              inside the zoom group so panning slides content over it. */}
          <pattern id="mg-dots" width="26" height="26" patternUnits="userSpaceOnUse">
            <circle cx="1.1" cy="1.1" r="1.1" style={{ fill: 'var(--border-soft)' }} />
          </pattern>
          <filter id="mg-node-shadow" x="-60%" y="-60%" width="220%" height="220%">
            <feDropShadow dx="0" dy="1.6" stdDeviation="2.4" flood-opacity="0.28" />
          </filter>
          <filter id="mg-edge-glow" x="-30%" y="-30%" width="160%" height="160%">
            <feDropShadow
              dx="0"
              dy="0"
              stdDeviation="2.4"
              style={{ floodColor: 'var(--accent)', floodOpacity: 0.65 }}
            />
          </filter>
          <marker
            id="mg-arr-ref"
            viewBox="0 0 10 10"
            refX="8.5"
            refY="5"
            markerWidth="9"
            markerHeight="9"
            markerUnits="userSpaceOnUse"
            orient="auto-start-reverse"
          >
            <path d="M 0.5 1 L 9 5 L 0.5 9 z" class="mg-arrow-ref" />
          </marker>
          <marker
            id="mg-arr-promo"
            viewBox="0 0 10 10"
            refX="8.5"
            refY="5"
            markerWidth="9"
            markerHeight="9"
            markerUnits="userSpaceOnUse"
            orient="auto-start-reverse"
          >
            <path d="M 0.5 1 L 9 5 L 0.5 9 z" class="mg-arrow-promo" />
          </marker>
        </defs>
        <rect width={size.w} height={size.h} fill="url(#mg-dots)" opacity="0.55" />
        <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
          <rect class="mg-bg" x={-size.w * 4} y={-size.h * 4} width={size.w * 9} height={size.h * 9} />
          {graph.edges.map((e, i) => {
            const a = pos.get(e.from)
            const b = pos.get(e.to)
            if (!a || !b) return null
            const dx = b.x - a.x
            const dy = b.y - a.y
            const dist = Math.max(Math.hypot(dx, dy), 1)
            const ux = dx / dist
            const uy = dy / dist
            // Endpoints pull back to the node rims so the arrowhead lands on
            // the boundary, not buried under the circle.
            const x1 = a.x + ux * (nodeRadius(a) + 2)
            const y1 = a.y + uy * (nodeRadius(a) + 2)
            const x2 = b.x - ux * (nodeRadius(b) + 7)
            const y2 = b.y - uy * (nodeRadius(b) + 7)
            const off = edgeSpread.get(i) ?? 16
            const mx = (x1 + x2) / 2 - uy * off
            const my = (y1 + y2) / 2 + ux * off
            const lit = hover !== null && (e.from === hover || e.to === hover)
            const dim = hover !== null && !lit
            // Quadratic Bézier point at t=0.5: (P0 + 2·C + P2) / 4 — the
            // badge sits on the curve itself, not the control point.
            const lx = (x1 + 2 * mx + x2) / 4
            const ly = (y1 + 2 * my + y2) / 4
            const d = `M ${x1} ${y1} Q ${mx} ${my} ${x2} ${y2}`
            const bw = 9 + String(e.weight ?? 0).length * 4.6
            return (
              <g
                key={i}
                class={`mg-edge-g${dim ? ' dim' : ''}${lit ? ' lit' : ''}`}
                style={{ animationDelay: `${Math.min(i * 18, 400)}ms` }}
              >
                <path
                  class={`mg-edge mg-edge-${e.kind}`}
                  style={{ strokeWidth: edgeWidth(e) + (lit ? 0.7 : 0) }}
                  d={d}
                  marker-end={`url(#mg-arr-${e.kind === 'promotion' ? 'promo' : 'ref'})`}
                />
                {/* Fat invisible stroke: a real hit target for the tooltip,
                    the thin visible line alone is hard to hover. */}
                <path class="mg-edge-hit" d={d}>
                  <title>
                    {t(`memory.graph.${e.kind}`, e.kind)}
                    {(e.weight ?? 0) > 1 ? ` ×${e.weight}` : ''}
                  </title>
                </path>
                {(e.weight ?? 0) > 1 && (
                  <g class="mg-badge">
                    <rect x={lx - bw / 2} y={ly - 6} width={bw} height={12} rx={6} />
                    <text class="mono" x={lx} y={ly + 3} text-anchor="middle">
                      ×{e.weight}
                    </text>
                  </g>
                )}
              </g>
            )
          })}
          {graph.nodes.map((n, i) => {
            const p = pos.get(n.id)!
            const dimmed = hover !== null && hover !== n.id && !neighbors.has(n.id)
            const r = nodeRadius(n)
            return (
              <g
                key={n.id}
                class={`mg-node ${KIND_CLASS[n.kind] ?? 'mg-core'}${dimmed ? ' dim' : ''}${
                  selected === n.id ? ' sel' : ''
                }${n.status && n.status !== 'active' ? ' pending' : ''}`}
                transform={`translate(${p.x},${p.y})`}
                onMouseEnter={() => setHover(n.id)}
                onMouseLeave={() => setHover((v) => (v === n.id ? null : v))}
                onFocus={() => setHover(n.id)}
                onBlur={() => setHover((v) => (v === n.id ? null : v))}
                onClick={() => setSelected(n.id === selected ? null : n.id)}
                role="button"
                tabIndex={0}
                aria-label={`${t(`memory.graph.${n.kind}`, n.kind)} ${n.label}`}
                aria-pressed={selected === n.id}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    setSelected(n.id === selected ? null : n.id)
                  } else if (e.key === 'Escape') {
                    setSelected(null)
                  }
                }}
              >
                {/* Inner g carries the pop-in — animating transform on the
                    outer one would fight its translate() attribute. */}
                <g class="mg-pop" style={{ animationDelay: `${Math.min(i * 30, 520)}ms` }}>
                  {selected === n.id && <circle class="mg-halo" r={r + 6} />}
                  <circle class="mg-hit" r={r + 10} />
                  <circle
                    class="mg-dot"
                    r={r}
                    fill={KIND_FILL[n.kind] ? `url(#mg-fill-${n.kind})` : undefined}
                    filter="url(#mg-node-shadow)"
                  />
                  <text y={r + 15} text-anchor="middle">
                    {n.label}
                  </text>
                </g>
              </g>
            )
          })}
        </g>
      </svg>
      <div class="mg-hint dim">{t('memory.graph.hint')}</div>
      <div class="mg-controls">
        <button
          type="button"
          class="btn small"
          aria-label={t('memory.graph.zoomOut')}
          onClick={() => zoomAt(size.w / 2, size.h / 2, 1 / 1.3)}
        >
          −
        </button>
        <button
          type="button"
          class="btn small"
          aria-label={t('memory.graph.zoomIn')}
          onClick={() => zoomAt(size.w / 2, size.h / 2, 1.3)}
        >
          +
        </button>
        {viewDirty && (
          <button type="button" class="btn small" onClick={() => setView(VIEW_HOME)}>
            {t('memory.graph.reset')}
          </button>
        )}
      </div>
      <div class="mg-legend">
        {(['core', 'topic', 'project', 'daily', 'skill'] as const).map((k) => (
          <span key={k} class={`mg-legend-item ${KIND_CLASS[k]}`}>
            <i /> {t(`memory.graph.${k}`)}
          </span>
        ))}
        <span class="mg-legend-item mg-legend-edge-promo">
          <svg width="22" height="8"><line x1="0" y1="4" x2="22" y2="4" /></svg>
          {t('memory.graph.promotion')}
        </span>
        <span class="mg-legend-item mg-legend-edge-ref">
          <svg width="22" height="8"><line x1="0" y1="4" x2="22" y2="4" /></svg>
          {t('memory.graph.reference')}
        </span>
      </div>
      {sel && (
        <div class={`mg-detail card mg-detail-${sel.kind}`}>
          <div class="mg-detail-head">
            <span class={`badge ${sel.kind === 'core' ? 'green' : sel.kind === 'project' ? 'blue' : sel.kind === 'skill' ? 'yellow' : ''}`}>
              {t(`memory.graph.${sel.kind}`)}
            </span>
            <strong class="mono">{sel.label}</strong>
            {sel.status && <span class="badge yellow">{sel.status}</span>}
            <button type="button" class="icon-btn" onClick={() => setSelected(null)} aria-label={t('common.close')}>×</button>
          </div>
          {(sel.entries || sel.files || sel.chars) && (
            <div class="mg-detail-stats dim">
              {sel.entries ? <span>{t('memory.graph.entriesN', { n: sel.entries })}</span> : null}
              {sel.files ? <span>{t('memory.graph.filesN', { n: sel.files })}</span> : null}
              {sel.chars ? <span>{t('memory.graph.charsN', { n: sel.chars })}</span> : null}
            </div>
          )}
          {selEdges.length > 0 ? (
            <ul class="mg-detail-edges">
              {selEdges.map((e, i) => {
                const other = e.from === sel.id ? e.to : e.from
                return (
                  <li key={i}>
                    <span class="dim">{e.from === sel.id ? '→' : '←'}</span>
                    <button type="button" class="mg-neighbor mono" onClick={() => setSelected(other)}>
                      {labelOf(other)}
                    </button>
                    <span class="dim">
                      · {t(`memory.graph.${e.kind}`)}
                      {(e.weight ?? 0) > 1 ? ` ×${e.weight}` : ''}
                    </span>
                  </li>
                )
              })}
            </ul>
          ) : (
            <p class="dim">{t('memory.graph.noEdges')}</p>
          )}
          {onOpen && sel.kind !== 'core' && (
            <button class="btn small" onClick={() => onOpen(sel)}>
              {t('memory.graph.open')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
