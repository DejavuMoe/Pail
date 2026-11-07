// Writes public/audio/events.json: what happens when, in video frames, for the
// sound script (audio/synth.mjs). The times come from src/timeline.js, the same
// file the picture is cut with.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { APP_START, FPS, INTRO, OUTRO_FRAMES, buildTimeline } from '../src/timeline.js'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'public/capture/manifest.json'), 'utf8'))
const T = buildTimeline(manifest)
const vn = (c) => Math.round(T.capToVid(c) * 2) / 2 // c in 30 fps frames (T.a); half-frame precision
const v = (c) => vn(c / T.cu) // c in frames of the capture

const events = []
for (const e of manifest.events) {
  if (e.kind === 'key' && e.name === 'palette') events.push({ frame: v(e.frame), kind: 'palette' })
  else if (e.kind === 'key' && e.name === 'enter') events.push({ frame: v(e.frame), kind: 'enter' })
  else if (e.kind === 'key') events.push({ frame: v(e.frame), kind: 'type' })
  else events.push({ frame: v(e.frame), kind: e.kind, name: e.name })
}
events.sort((a, b) => a.frame - b.frame)

const wipeStart = vn(T.a.theme)
const out = {
  fps: FPS,
  totalFrames: T.total,
  seconds: T.total / FPS,
  intro: { ...INTRO, appRise: APP_START },
  outro: { start: T.outroStart, frames: OUTRO_FRAMES },
  wipe: { start: wipeStart, end: wipeStart + 24 },
  // the transfers of the upload scene: from the drop to the end of the last one
  upload: { start: vn(T.a.drop), end: vn(T.a.uploadDone) },
  events,
}
fs.mkdirSync(path.join(root, 'public/audio'), { recursive: true })
fs.writeFileSync(path.join(root, 'public/audio/events.json'), JSON.stringify(out, null, 1))
console.log(`events.json: ${events.length} events, ${out.totalFrames} frames (${out.seconds.toFixed(1)} s)`)
