// The one place that says when things happen. The capture (go test -run
// TestFilm) is one continuous recording of the real app; its frames are
// numbered "capture frames" (c). The video adds an intro before it and an outro
// after it, and may play parts of the capture faster or slower. Remotion and the
// sound script (scripts/events.mjs) both read this file, so the picture and the
// sound cannot drift apart.

export const FPS = 30
export const WIDTH = 1920
export const HEIGHT = 1080

// Video frame at which capture frame 0 is shown: the app rises while the intro
// is still on screen.
export const APP_START = 96
export const OUTRO_FRAMES = 150
export const OUTRO_OVERLAP = 22 // the outro starts this early, over the last capture frames

// The intro, in video frames: the icon appears, the three strata of the icon
// fill as the object keys land, then the name and the line under it.
export const INTRO = { icon: 8, strata: [30, 40, 50], title: 58, tagline: 74, providers: 92 }

// The window is 1440x900 DIPs; this is how many canvas pixels one DIP is at zoom 1.
export const BASE_SCALE = 1.0
// Canvas pixel (x, y) the camera focus lands on.
export const FOCUS_X = WIDTH / 2
export const FOCUS_Y = 468

// The picture is rendered at OUT_FPS, but everything below is written (and the
// sound is cut) in frames of a 30 fps film: "video frame" and "capture frame"
// mean frames of that film, and may be fractions. SUB output frames make one.
export const OUT_FPS = 60
export const SUB = OUT_FPS / FPS

export function buildTimeline(manifest) {
  // The capture may have been recorded at a higher rate; bring it to 30 fps units.
  const cu = (manifest.fps ?? FPS) / FPS
  const m = {
    frames: { length: manifest.frames.length / cu },
    events: manifest.events.map((e) => ({ ...e, frame: e.frame / cu })),
    scenes: Object.fromEntries(Object.entries(manifest.scenes).map(([k, [a, b]]) => [k, [a / cu, b / cu]])),
  }
  const ev = (kind, name, nth = 0) => {
    const hit = m.events.filter((e) => e.kind === kind && (name == null || e.name === name))[nth]
    if (!hit) throw new Error(`timeline: no event ${kind} ${name} #${nth}`)
    return hit
  }
  const at = (kind, name, nth) => ev(kind, name, nth).frame
  const N = m.frames.length

  const a = {
    bucket: at('click', 'acme-assets'),
    hero: at('click', 'hero.png'),
    preview: at('click', 'Preview'),
    uploadStart: m.scenes.upload[0],
    drop: at('drop', 'files'),
    uploadDone: at('done', 'uploads'),
    team: at('click', 'team-photo.png'),
    palette: at('key', 'palette', 0),
    generate: at('click', 'Generate'),
    copy: at('click', 'Copy'),
    close: at('click', 'Close'),
    vBucket: at('click', 'acme-versioned'),
    notes: at('click', 'notes.txt'),
    verTab: at('click', 'Versions'),
    restore: at('click', 'Restore this version'),
    restored: at('done', 'restored'),
    palette2: at('key', 'palette', 1),
    theme: at('theme', 'dark'),
  }

  // ---- capture frame -> video frame ---------------------------------------------------------
  // Pieces of the capture and how many video frames each takes. Pieces that
  // are not listed play at normal speed.
  const pieces = [
    { from: a.preview + 40, to: a.uploadStart, frames: 40 }, // the preview, held
  ]
  const segs = []
  let cap = 0
  let vid = 0
  for (const p of pieces.sort((x, y) => x.from - y.from)) {
    if (p.from > cap) {
      segs.push({ c0: cap, c1: p.from, v0: vid, v1: vid + (p.from - cap) })
      vid += p.from - cap
    }
    segs.push({ c0: p.from, c1: p.to, v0: vid, v1: vid + p.frames })
    vid += p.frames
    cap = p.to
  }
  segs.push({ c0: cap, c1: N, v0: vid, v1: vid + (N - cap) })
  const capEndVid = vid + (N - cap)

  const capToVid = (c) => {
    for (const s of segs) if (c <= s.c1) return APP_START + s.v0 + ((c - s.c0) / (s.c1 - s.c0)) * (s.v1 - s.v0)
    return APP_START + capEndVid
  }
  const vidToCap = (v) => {
    const x = v - APP_START
    if (x <= 0) return 0
    for (const s of segs) if (x <= s.v1) return s.c0 + ((x - s.v0) / (s.v1 - s.v0)) * (s.c1 - s.c0)
    return N - 1
  }
  const outroStart = Math.round(APP_START + capEndVid - OUTRO_OVERLAP)
  const total = outroStart + OUTRO_FRAMES

  // ---- the camera ----------------------------------------------------------------------------
  // Keyframes: the camera is at (x, y) DIPs with zoom z at capture frame c; it
  // starts moving `lead` frames before.
  const cam = [
    { c: 0, x: 720, y: 450, z: 1.0 },
    { c: a.bucket - 6, x: 560, y: 300, z: 1.32, lead: 40 },
    { c: a.hero, x: 640, y: 300, z: 1.5, lead: 36 },
    { c: a.preview + 22, x: 1130, y: 330, z: 1.85, lead: 34 },
    { c: a.uploadStart + 4, x: 720, y: 450, z: 1.0, lead: 44 },
    { c: a.drop + 30, x: 720, y: 600, z: 1.3, lead: 34 },
    { c: a.uploadDone + 6, x: 720, y: 440, z: 1.2, lead: 36 },
    { c: a.team, x: 760, y: 320, z: 1.4, lead: 40 },
    { c: a.palette + 24, x: 720, y: 250, z: 1.75, lead: 30 },
    { c: a.generate - 36, x: 720, y: 400, z: 1.85, lead: 24 },
    { c: a.copy, x: 720, y: 420, z: 2.1, lead: 40 },
    { c: a.close + 8, x: 720, y: 450, z: 1.1, lead: 34 },
    { c: a.vBucket - 4, x: 440, y: 270, z: 1.4, lead: 38 },
    { c: a.notes, x: 700, y: 220, z: 1.45, lead: 34 },
    { c: a.verTab + 18, x: 1160, y: 260, z: 2.0, lead: 36 },
    { c: a.restored + 40, x: 1160, y: 260, z: 2.0, lead: 10 },
    { c: a.palette2 - 24, x: 720, y: 450, z: 1.1, lead: 40 },
    { c: a.theme + 20, x: 720, y: 450, z: 1.0, lead: 40 },
  ]

  // ---- captions (capture frames) ------------------------------------------------------------
  const captions = [
    { from: a.bucket - 20, to: a.uploadStart - 4, en: 'Browse buckets and preview files', zh: '浏览存储桶，预览文件' },
    { from: a.drop - 78, to: a.uploadDone + 52, en: 'Upload files and folders, or drag them in', zh: '上传文件和文件夹，或直接拖进窗口' },
    { from: a.palette - 6, to: a.close + 6, en: 'Presigned links from the command palette', zh: '用命令面板生成预签名链接' },
    { from: a.vBucket + 12, to: a.restored + 70, en: 'Restore an older version', zh: '恢复旧版本' },
    { from: a.theme - 6, to: N - 6, en: 'Light and dark themes', zh: '亮色与暗色主题' },
  ]

  return { a, N, cu, ev, segs, capToVid, vidToCap, cam, captions, outroStart, total, capEndVid }
}

// camAt gives the camera pose at capture frame c.
export function camAt(keys, c) {
  const ease = (t) => t * t * t * (t * (t * 6 - 15) + 10)
  let last = 0 // the last keyframe whose move has begun
  for (let i = 1; i < keys.length; i++) if (c >= keys[i].c - (keys[i].lead ?? 30)) last = i
  const k = keys[last]
  if (last === 0) return { x: k.x, y: k.y, z: k.z }
  if (c >= k.c) return { x: k.x, y: k.y, z: k.z }
  // A move that began before the one before it had finished starts from where
  // the camera was at that moment.
  const t0 = k.c - (k.lead ?? 30)
  const from = camAt(keys.slice(0, last), t0)
  const e = ease((c - t0) / (k.lead ?? 30))
  return { x: from.x + (k.x - from.x) * e, y: from.y + (k.y - from.y) * e, z: from.z + (k.z - from.z) * e }
}
