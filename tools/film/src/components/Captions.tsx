import { C, FONT, clamp, easeOut } from '../theme'
import { T } from '../data'

// Bilingual caption: the English line large, the Chinese line under it.
export const Captions = ({ v }: { v: number }) => {
  const cap = T.captions.find((x: { from: number; to: number }) => {
    const a = T.capToVid(x.from)
    const b = T.capToVid(x.to)
    return v >= a && v < b
  })
  if (!cap) return null
  const a = T.capToVid(cap.from)
  const b = T.capToVid(cap.to)
  const inT = easeOut((v - a) / 12)
  const outT = easeOut((b - v) / 10)
  const o = clamp(Math.min(inT, outT))
  return (
    <div style={{ position: 'absolute', left: 0, right: 0, bottom: 30, display: 'flex', justifyContent: 'center', opacity: o, transform: `translateY(${(1 - o) * 16}px)` }}>
      <div
        style={{
          fontFamily: FONT, textAlign: 'center', background: 'rgba(24,23,21,0.94)', border: '1px solid rgba(255,255,255,0.14)',
          borderRadius: 22, padding: '12px 30px 14px', boxShadow: '0 12px 40px rgba(0,0,0,0.25)',
        }}
      >
        <div style={{ fontSize: 38, fontWeight: 600, color: '#F5F3EE', letterSpacing: '-0.01em' }}>{cap.en}</div>
        <div style={{ fontSize: 25, color: '#BDB9B0', marginTop: 2 }}>{cap.zh}</div>
      </div>
    </div>
  )
}

// A small note that what is on screen is the real app.
export const Honest = ({ v, from, to, dark }: { v: number; from: number; to: number; dark: boolean }) => {
  const o = clamp(Math.min((v - from) / 15, (to - v) / 15)) * 0.75
  return (
    <div style={{ position: 'absolute', right: 24, bottom: 14, fontFamily: FONT, fontSize: 15, color: dark ? C.inkMuted : C.muted, opacity: o, textAlign: 'right', lineHeight: 1.35 }}>
      Real app · in-memory demo S3
      <br />
      真实应用 · 内存 S3 演示数据
    </div>
  )
}
