import type { ReactNode } from 'react'
import { Img, staticFile } from 'remotion'
import { C } from '../theme'
import { BASE_SCALE, FOCUS_X, FOCUS_Y, HEIGHT, WIDTH } from '../timeline.js'

export type Pose = { x: number; y: number; z: number }

const WIN_W = 1440
const WIN_H = 900

// Keeps the view inside the window (plus a margin) so the camera never shows
// the backdrop beyond the window's own edge by more than a sliver.
export const clampPose = (p: Pose): Pose => {
  const s = BASE_SCALE * p.z
  const hw = FOCUS_X / s
  const up = FOCUS_Y / s
  const down = (HEIGHT - FOCUS_Y) / s
  const fit = (v: number, lo: number, hi: number, mid: number) => (lo > hi ? mid : Math.min(hi, Math.max(lo, v)))
  return {
    z: p.z,
    x: fit(p.x, hw - 24, WIN_W - hw + 24, WIN_W / 2),
    y: fit(p.y, up - 24, WIN_H - down + 24, WIN_H / 2),
  }
}

// One full-canvas layer: a backdrop and the window showing one captured frame,
// placed by the camera. Two of these (light and dark) are stacked for the wipe.
export const Stage = ({
  file, backdrop, pose, enter, exit, clip, children,
}: {
  file: string
  backdrop: string
  pose: Pose
  enter: number // 0..1: the window rises into place
  exit: number // 0..1: the window shrinks away
  clip?: string
  children?: ReactNode
}) => {
  const p = clampPose(pose)
  const s = BASE_SCALE * p.z
  const tx = FOCUS_X - p.x * s
  const ty = FOCUS_Y - p.y * s
  const rx = 42 * Math.pow(1 - enter, 1.4)
  const rise = 200 * (1 - enter)
  const sc = (0.88 + 0.12 * enter) * (1 - 0.45 * exit)
  const op = Math.min(1, enter * 3) * (1 - exit)
  return (
    <div style={{ position: 'absolute', inset: 0, clipPath: clip, WebkitClipPath: clip }}>
      <div style={{ position: 'absolute', inset: 0, background: backdrop, opacity: Math.min(1, enter * 1.6) * (1 - exit) }} />
      <div style={{ position: 'absolute', inset: 0, perspective: 2400, perspectiveOrigin: `${WIDTH / 2}px 560px` }}>
        <div style={{ position: 'absolute', inset: 0, transformOrigin: `${WIDTH / 2}px 620px`, transform: `translateY(${rise}px) rotateX(${rx}deg) scale(${sc})`, opacity: op }}>
          <div
            style={{
              position: 'absolute', left: 0, top: 0, width: WIN_W, height: WIN_H, transformOrigin: '0 0',
              transform: `translate(${tx}px, ${ty}px) scale(${s})`, borderRadius: 10, boxShadow: '0 26px 70px rgba(20,19,17,0.30), 0 0 0 1px rgba(20,19,17,0.10)', background: C.paper,
            }}
          >
            <Img src={staticFile(`capture/${file}`)} style={{ width: WIN_W, height: WIN_H, display: 'block', borderRadius: 10 }} />
            {children}
          </div>
        </div>
      </div>
    </div>
  )
}
