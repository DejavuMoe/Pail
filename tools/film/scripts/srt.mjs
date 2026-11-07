// Writes out/pail-demo.en.srt and out/pail-demo.zh-Hans.srt from the captions in
// src/timeline.js (the same ones the video burns in), plus the intro and outro lines.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { FPS, OUTRO_FRAMES, buildTimeline } from '../src/timeline.js'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'public/capture/manifest.json'), 'utf8'))
const T = buildTimeline(manifest)

const cues = [
  { from: 62, to: 118, en: 'A desktop client for S3‑compatible storage', zh: 'S3 兼容存储的桌面客户端' },
  ...T.captions.map((c) => ({ from: Math.round(T.capToVid(c.from)), to: Math.round(T.capToVid(c.to)), en: c.en, zh: c.zh })),
  { from: T.outroStart + 36, to: T.outroStart + OUTRO_FRAMES, en: 'Free and open source', zh: '免费开源' },
]

for (let i = 0; i + 1 < cues.length; i++) cues[i].to = Math.min(cues[i].to, cues[i + 1].from - 1) // no overlaps

const ts = (f) => {
  const ms = Math.round((f / FPS) * 1000)
  const p = (n, w = 2) => String(n).padStart(w, '0')
  return `${p(Math.floor(ms / 3600000))}:${p(Math.floor(ms / 60000) % 60)}:${p(Math.floor(ms / 1000) % 60)},${p(ms % 1000, 3)}`
}
const out = path.join(root, 'out')
fs.mkdirSync(out, { recursive: true })
for (const [lang, name] of [['en', 'pail-demo.en.srt'], ['zh', 'pail-demo.zh-Hans.srt']]) {
  const body = cues.map((c, i) => `${i + 1}\n${ts(c.from)} --> ${ts(c.to)}\n${c[lang]}\n`).join('\n')
  fs.writeFileSync(path.join(out, name), body)
}
console.log(`wrote ${cues.length} cues`)
