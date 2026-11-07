import { useCurrentFrame, spring } from 'remotion'
import { C, FONT, MONO, clamp, easeInOut, easeOut } from '../theme'
import { Icon } from './Icon'
import { APP_START, INTRO, OUT_FPS, SUB } from '../timeline.js'

// Object keys of the demo bucket fall into the icon and fill its three strata.
const KEYS = [
  'assets/logo-dark.png', 'backups/2026/db-2026-10.sql.gz', 'website/index.html', 'reports/q3-financials.pdf',
  'assets/video/launch-teaser.mp4', 'config/app.yaml', 'logs/app/app-2026-10-12.jsonl', 'README.txt',
  'manifest.json', 'assets/fonts/inter.woff2', 'reports/customers-export.csv', 'assets/images/hero.png',
  'notes.txt', 'website/css/app.3f9a1c.css', 'backups/2026/files-2026-10.tar.zst',
]

const CX = 960
const ICON_Y = 372 // icon center
const ICON = 220

export const Intro = () => {
  const raw = useCurrentFrame()
  const f = raw / SUB // frames of the 30 fps timeline
  // springs are stepped by whole frames, so they get the real frame and rate
  const sp = (at: number, config: { damping: number; stiffness: number }) => spring({ frame: raw - at * SUB, fps: OUT_FPS, config })

  // keys
  const keys = KEYS.map((k, i) => {
    const land = 14 + i * 2.4
    const start = land - 26
    const t = (f - start) / 26
    if (t <= 0 || t >= 1.02) return null
    const side = (i % 2 ? 1 : -1) * (260 + ((i * 97) % 380))
    const x = CX + side * (1 - easeInOut(t * 1.05)) * 1.0
    const y = -60 + (ICON_Y - ICON * 0.38 + 60) * t * t // gravity
    const shrink = 1 - clamp((t - 0.82) / 0.2)
    return (
      <div
        key={k}
        style={{
          position: 'absolute', left: x, top: y, transform: `translate(-50%, -50%) scale(${shrink})`,
          opacity: clamp(t * 6) * shrink, fontFamily: MONO, fontSize: 32, whiteSpace: 'nowrap',
          color: i % 3 === 0 ? C.accentLight : C.inkMuted,
        }}
      >
        {k}
      </div>
    )
  })

  const iconIn = sp(INTRO.icon, { damping: 14, stiffness: 120 })
  const bump = INTRO.strata.reduce((s, at) => s + 0.045 * Math.exp(-Math.max(0, f - at) / 5) * (f >= at ? 1 : 0), 0)
  const fill = INTRO.strata.map((at) => sp(at, { damping: 15, stiffness: 160 })) as [number, number, number]

  const rise = (at: number, d = 16) => ({ opacity: easeOut((f - at) / d), transform: `translateY(${(1 - easeOut((f - at) / d)) * 26}px)` })

  // leaving: the window rises over this
  const leave = easeInOut((f - (APP_START + 4)) / 30)

  return (
    <div style={{ position: 'absolute', inset: 0, opacity: 1 - leave, transform: `translateY(${-70 * leave}px) scale(${1 - 0.06 * leave})` }}>
      {keys}
      <div style={{ position: 'absolute', left: CX - ICON / 2, top: ICON_Y - ICON / 2, transform: `scale(${(0.7 + 0.3 * iconIn) * (1 + bump)})`, opacity: clamp(iconIn * 2) }}>
        <Icon size={ICON} fill={fill} />
      </div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 520, textAlign: 'center', fontFamily: FONT, ...rise(INTRO.title) }}>
        <div style={{ fontSize: 136, fontWeight: 700, letterSpacing: '-0.035em', color: C.inkText, lineHeight: 1 }}>Pail</div>
      </div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 690, textAlign: 'center', fontFamily: FONT }}>
        <div style={{ fontSize: 44, fontWeight: 500, color: '#D5D2CB', ...rise(INTRO.tagline) }}>A desktop client for S3‑compatible storage</div>
        <div style={{ fontSize: 30, marginTop: 8, color: C.inkSubtle, ...rise(INTRO.tagline + 6) }}>S3 兼容存储的桌面客户端</div>
      </div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 850, textAlign: 'center', fontFamily: FONT, fontSize: 28, color: C.accentLight, ...rise(INTRO.providers) }}>
        Amazon S3 · Cloudflare R2 · MinIO
      </div>
    </div>
  )
}
