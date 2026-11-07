// Makes docs/video/pail-demo.webp, the cover of the video on the website: one
// frame of the real app (the capture from `go test -run TestFilm`, taken just
// once the dark theme is on), full-bleed, with
// a play button. Needs ffmpeg and public/capture/.
//
//	node scripts/poster.mjs [out.webp]
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const out = path.resolve(process.argv[2] ?? path.join(root, '../../docs/video/pail-demo.webp'))
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'public/capture/manifest.json'), 'utf8'))
const theme = manifest.events.find((e) => e.kind === 'theme')
const frame = path.join(root, 'public/capture', manifest.frames[theme.frame + 90]) // the dark theme, the palette closed

const W = 1920, H = 1080
const cx = W / 2, cy = H / 2, R = 64 // play button

// Triangle, points right, centred optically a little right of the circle's centre.
const tri = [[cx - 18, cy - 29], [cx - 18, cy + 29], [cx + 32, cy]]
const edge = (a, b) => {
  const len = Math.hypot(b[0] - a[0], b[1] - a[1])
  // distance inside the edge a->b (positive inside for this winding)
  return `(-((${b[0] - a[0]})*(Y-${a[1]})-(${b[1] - a[1]})*(X-${a[0]}))/${len.toFixed(4)})`
}
const triMask = `255*clip(min(${edge(tri[0], tri[1])},min(${edge(tri[1], tri[2])},${edge(tri[2], tri[0])}))+0.5,0,1)`
const circleMask = `240*clip(${R}+0.5-hypot(X-${cx},Y-${cy}),0,1)`

// The 1440x900 window fills the 16:9 frame to its edges (its top 810 points), so
// the cover has no side bars; a flat dark layer keeps the button readable.
const graph = [
  `[0:v]scale=${W}:-2:flags=lanczos,crop=${W}:${H}:0:0,format=rgba[app]`,
  `color=c=black@0.22:s=${W}x${H}:d=1,format=rgba[dim]`,
  `[app][dim]overlay=0:0:format=auto[a]`,
  `color=c=white:s=${W}x${H}:d=1,format=rgba[w]`,
  `nullsrc=s=${W}x${H}:d=1,format=gray,geq=lum='${circleMask}'[cm]`,
  `[w][cm]alphamerge[circle]`,
  `[a][circle]overlay=0:0:format=auto[b]`,
  `color=c=0x1D1C1A:s=${W}x${H}:d=1,format=rgba[k]`,
  `nullsrc=s=${W}x${H}:d=1,format=gray,geq=lum='${triMask}'[tm]`,
  `[k][tm]alphamerge[triangle]`,
  `[b][triangle]overlay=0:0:format=auto,format=yuv420p`,
].join(';')
fs.mkdirSync(path.dirname(out), { recursive: true })
execFileSync('ffmpeg', ['-y', '-v', 'error', '-i', frame, '-filter_complex', graph, '-frames:v', '1', '-c:v', 'libwebp', '-quality', '82', out], { stdio: 'inherit' })
console.log(`wrote ${out} from ${path.basename(frame)}`)
