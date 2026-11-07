import { C, FONT, MONO, clamp, easeInOut, easeOut } from '../theme'
import { T, manifest } from '../data'

// Everything drawn over the app's own frames, in the window's coordinates (DIPs):
// the cursor, the ring where a click lands, and the files flying in before a drop.

const arrow = 'M1 1 L1 17.5 L5.4 13.7 L8.3 20.4 L11 19.2 L8.2 12.6 L14 12.6 Z'

// Catmull-Rom through four neighbouring samples: a smooth path, not a polyline.
const spline = (p0: number, p1: number, p2: number, p3: number, t: number) =>
  0.5 * (2 * p1 + (-p0 + p2) * t + (2 * p0 - 5 * p1 + 4 * p2 - p3) * t * t + (-p0 + 3 * p1 - 3 * p2 + p3) * t * t * t)

// c is a frame number of the capture (not of the 30 fps timeline).
export const Cursor = ({ c, z }: { c: number; z: number }) => {
  const n = manifest.cursor.length
  const at = (i: number) => manifest.cursor[Math.min(n - 1, Math.max(0, i))]
  const i1 = Math.min(n - 1, Math.max(0, Math.floor(c)))
  const f = c - i1
  const a = at(i1)
  const b = at(i1 + 1)
  const show = Math.min(a[3], b[3])
  if (!show) return null
  const x = spline(at(i1 - 1)[0], a[0], b[0], at(i1 + 2)[0], f)
  const y = spline(at(i1 - 1)[1], a[1], b[1], at(i1 + 2)[1], f)
  const down = a[2] > 0.5
  const k = (1.2 / z) * (down ? 0.88 : 1)
  return (
    <svg
      width="28" height="28" viewBox="0 0 28 28"
      style={{ position: 'absolute', left: x, top: y, overflow: 'visible', transform: `scale(${k})`, transformOrigin: '1px 1px', filter: 'drop-shadow(0 2px 3px rgba(0,0,0,0.35))' }}
    >
      <path d={arrow} fill="#FFFFFF" stroke="#1D1C1A" strokeWidth="1.5" strokeLinejoin="round" />
    </svg>
  )
}

export const Clicks = ({ c, z }: { c: number; z: number }) => (
  <>
    {manifest.events
      .filter((e) => e.kind === 'click' && c >= e.frame && c < e.frame + 20 * T.cu)
      .map((e, i) => {
        const t = (c - e.frame) / (20 * T.cu)
        const r = (10 + 30 * easeOut(t)) / z
        return (
          <div
            key={i}
            style={{
              position: 'absolute', left: (e.x ?? 0) - r, top: (e.y ?? 0) - r, width: r * 2, height: r * 2, borderRadius: '50%',
              border: `${2.5 / z}px solid ${C.accent}`, opacity: 0.8 * (1 - t), boxSizing: 'border-box',
            }}
          />
        )
      })}
  </>
)

// Three files, as they are named in the capture's temporary folder.
const CARDS = [
  { name: 'team-photo.png', size: '17.2 KB', tint: '#9ED8C6', to: [650, 420], from: [1790, 90], rot: -7 },
  { name: 'sunset.png', size: '18.8 KB', tint: '#F2B6A0', to: [740, 470], from: [1830, 420], rot: 6 },
  { name: 'launch-trailer.mp4', size: '96.0 MB', tint: '#2A2926', to: [830, 430], from: [1780, 760], rot: -4, play: true },
]

export const FileCards = ({ c }: { c: number }) => {
  const drop = T.a.drop
  return (
    <>
      {CARDS.map((k, i) => {
        const start = drop - 82 + i * 9
        const t = (c - start) / (drop - 6 - start - i * 3)
        if (c < start || c > drop + 10) return null
        const e = easeOut(t)
        const x = k.from[0] + (k.to[0] - k.from[0]) * e
        const y = k.from[1] + (k.to[1] - k.from[1]) * e - Math.sin(Math.PI * clamp(t)) * 70
        const absorb = clamp((c - drop) / 9)
        const s = (1 - 0.65 * easeInOut(absorb)) * (0.92 + 0.08 * e)
        return (
          <div
            key={k.name}
            style={{
              position: 'absolute', left: x - 84, top: y - 58, width: 168, height: 116, borderRadius: 12, background: '#FFFFFF',
              border: `1px solid ${C.border}`, boxShadow: '0 14px 34px rgba(20,19,17,0.22)', overflow: 'hidden',
              transform: `rotate(${k.rot * (1 - e)}deg) scale(${s})`, opacity: 1 - absorb, fontFamily: FONT,
            }}
          >
            <div style={{ height: 70, background: k.tint, position: 'relative' }}>
              {k.play && <div style={{ position: 'absolute', left: 74, top: 21, borderLeft: '26px solid #F5F3EE', borderTop: '14px solid transparent', borderBottom: '14px solid transparent' }} />}
            </div>
            <div style={{ padding: '6px 10px' }}>
              <div style={{ fontFamily: MONO, fontSize: 12.5, color: C.text, whiteSpace: 'nowrap' }}>{k.name}</div>
              <div style={{ fontSize: 11, color: C.muted, marginTop: 1 }}>{k.size}</div>
            </div>
          </div>
        )
      })}
    </>
  )
}
