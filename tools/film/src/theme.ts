// Colors are the Graphite & Paper tokens of cmd/pail-desktop/theme.go.
export const C = {
  paper: '#FBFAF8',
  sidebar: '#F3F1EC',
  border: '#E7E4DE',
  text: '#1D1C1A',
  muted: '#6B675F',
  accent: '#34598F',
  // dark
  graphite: '#1A1A18',
  graphiteDeep: '#121210',
  tile: '#1D1C1A',
  inkText: '#EAE8E3',
  inkMuted: '#A29E96',
  inkSubtle: '#85817A',
  accentLight: '#86A8D8',
  accentMid: '#5C82BA',
  // the backdrop behind the window while it shows each theme
  stageLight: '#E8E4DA',
  stageDark: '#121210',
}

// System fonts only, as the app and the site do; no webfonts.
export const FONT =
  '"Segoe UI", "SF Pro Text", "PingFang SC", "Microsoft YaHei", "Noto Sans CJK SC", "Noto Sans", system-ui, sans-serif'
export const MONO = '"Cascadia Mono", "SF Mono", ui-monospace, Consolas, "DejaVu Sans Mono", monospace'

export const clamp = (x: number, a = 0, b = 1) => Math.min(b, Math.max(a, x))
export const easeOut = (t: number) => 1 - Math.pow(1 - clamp(t), 3)
export const easeInOut = (t: number) => {
  const x = clamp(t)
  return x * x * x * (x * (x * 6 - 15) + 10)
}
