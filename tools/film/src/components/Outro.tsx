import { useCurrentFrame, spring } from 'remotion'
import { C, FONT, MONO, easeOut } from '../theme'
import { Icon } from './Icon'
import { OUT_FPS, SUB } from '../timeline.js'

// The window has shrunk away; the icon, where to get Pail, and one line.
export const Outro = () => {
  const raw = useCurrentFrame()
  const f = raw / SUB // 0 at the start of the outro, in frames of the 30 fps timeline
  const rise = (at: number, d = 16) => ({ opacity: easeOut((f - at) / d), transform: `translateY(${(1 - easeOut((f - at) / d)) * 22}px)` })
  const pop = spring({ frame: raw - 10 * SUB, fps: OUT_FPS, config: { damping: 13, stiffness: 110 } })
  return (
    <div style={{ position: 'absolute', inset: 0, fontFamily: FONT, textAlign: 'center' }}>
      <div style={{ position: 'absolute', left: 960 - 90, top: 150, transform: `scale(${0.6 + 0.4 * pop})`, opacity: Math.min(1, pop * 2) }}>
        <Icon size={180} />
      </div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 360, fontSize: 112, fontWeight: 700, letterSpacing: '-0.035em', color: C.inkText, lineHeight: 1, ...rise(22) }}>Pail</div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 500, ...rise(36) }}>
        <div style={{ fontSize: 44, fontWeight: 500, color: '#D5D2CB' }}>Free and open source</div>
        <div style={{ fontSize: 30, marginTop: 6, color: C.inkSubtle }}>免费开源</div>
      </div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 676, fontSize: 32, color: C.inkMuted, ...rise(52) }}>Windows · macOS · Linux</div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 770, fontSize: 52, fontWeight: 600, color: C.accentLight, ...rise(66) }}>pail.download</div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 850, fontFamily: MONO, fontSize: 26, color: C.inkSubtle, ...rise(78) }}>github.com/DejavuMoe/Pail</div>
    </div>
  )
}
