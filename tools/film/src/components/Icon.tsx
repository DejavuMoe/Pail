import { C } from '../theme'

// The Pail icon (assets/brand/pail-icon.svg): a graphite tile with three
// strata that taper into a bucket. `fill` is how far each stratum has grown
// (0..1), so the intro can fill them one at a time.
export const Icon = ({ size, fill = [1, 1, 1] }: { size: number; fill?: [number, number, number] }) => {
  const strata = [
    { x: 44, y: 61, w: 168, c: C.accentLight, dot: { cx: 190, cy: 81, o: 1 } },
    { x: 58, y: 108, w: 140, c: C.accentMid, dot: { cx: 176, cy: 128, o: 0.85 } },
    { x: 72, y: 155, w: 112, c: C.accent, dot: { cx: 162, cy: 175, o: 0.7 } },
  ]
  return (
    <svg width={size} height={size} viewBox="0 0 256 256" style={{ display: 'block' }}>
      <rect width="256" height="256" rx="56" fill={C.tile} />
      <rect x="1.5" y="1.5" width="253" height="253" rx="54.5" fill="none" stroke="#FFFFFF" strokeOpacity="0.09" strokeWidth="3" />
      {strata.map((s, i) => {
        const f = fill[i]
        const w = Math.max(0.001, s.w * f)
        const x = s.x + (s.w - w) / 2 // grows from the middle
        return (
          <g key={i}>
            <rect x={x} y={s.y} width={w} height="40" rx="20" fill={s.c} />
            <circle cx={s.dot.cx} cy={s.dot.cy} r={6 * f} fill="#F3F1EC" fillOpacity={s.dot.o * f} />
          </g>
        )
      })}
    </svg>
  )
}
