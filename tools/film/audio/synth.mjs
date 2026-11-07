// Synthesises the soundtrack of the Pail demo film: music bed + frame-exact UI sounds.
//
//   node audio/synth.mjs      (reads public/audio/events.json, writes public/audio/score.wav)
//
// Everything is generated here: no samples, no dependencies, no network. A seeded PRNG
// makes every run byte-identical. Key: D major. Tempo: 120 BPM (bar = 2 s), stretched
// slightly if needed so that intro.appRise and outro.start both fall on a downbeat.
// Output: 48 kHz, 16-bit, stereo, normalised to -14 LUFS integrated, true peak <= -1 dBTP.

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const started = Date.now();
const FILM = join(dirname(fileURLToPath(import.meta.url)), '..');
const AUDIO_DIR = join(FILM, 'public', 'audio');
const E = JSON.parse(readFileSync(join(AUDIO_DIR, 'events.json'), 'utf8'));

const SR = 48000;
const FPS = E.fps;
const T = (frame) => frame / FPS; // video frame -> seconds
const DUR = E.totalFrames / FPS;
const N = Math.round(DUR * SR);
const TAU = Math.PI * 2;

// ---------------------------------------------------------------------------
// Deterministic randomness: one independent stream per purpose.
function rngFor(name) {
  let s = 0x9e3779b9;
  for (const c of name) s = Math.imul(s ^ c.charCodeAt(0), 0x85ebca6b) >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------------------------------------------------------------------------
// Pitch helpers (D major).
const PC = { C: 0, 'C#': 1, D: 2, 'D#': 3, E: 4, F: 5, 'F#': 6, G: 7, 'G#': 8, A: 9, 'A#': 10, B: 11 };
const midi = (name) => {
  const m = /^([A-G]#?)(-?\d)$/.exec(name);
  return PC[m[1]] + 12 * (Number(m[2]) + 1);
};
const mhz = (m) => 440 * 2 ** ((m - 69) / 12);
const hz = (name) => mhz(midi(name));
const D_MAJOR = [2, 4, 6, 7, 9, 11, 1];
// Diatonic step within D major (used for a harmony line a third below the hook).
function diatonic(m, steps) {
  const pc = D_MAJOR[(D_MAJOR.indexOf(m % 12) + steps + 70) % 7];
  let x = steps < 0 ? m - 1 : m + 1;
  while (x % 12 !== pc) x += steps < 0 ? -1 : 1;
  return x;
}

// ---------------------------------------------------------------------------
// Buses (stereo float buffers) and mixing helpers.
const bus = () => ({ L: new Float32Array(N), R: new Float32Array(N) });
const pad = bus(), bass = bus(), drums = bus(), keys = bus(), tex = bus(), sfx = bus();
const revSend = bus(); // into the reverb
const dlySend = bus(); // into the ping-pong delay

function panGains(pan) {
  const a = ((pan + 1) * Math.PI) / 4;
  return [Math.cos(a) * Math.SQRT2, Math.sin(a) * Math.SQRT2];
}
// Add a mono sound at time t (s) to a bus, with optional reverb / delay sends.
function addMono(b, t, x, gain = 1, pan = 0, rev = 0, dly = 0) {
  const [gl, gr] = panGains(pan);
  const o = Math.round(t * SR);
  for (let i = Math.max(0, -o); i < x.length && o + i < N; i++) {
    const v = x[i] * gain;
    b.L[o + i] += v * gl;
    b.R[o + i] += v * gr;
    if (rev) { revSend.L[o + i] += v * gl * rev; revSend.R[o + i] += v * gr * rev; }
    if (dly) { dlySend.L[o + i] += v * gl * dly; dlySend.R[o + i] += v * gr * dly; }
  }
}
function addStereo(b, t, xl, xr, gain = 1, rev = 0) {
  const o = Math.round(t * SR);
  for (let i = Math.max(0, -o); i < xl.length && o + i < N; i++) {
    b.L[o + i] += xl[i] * gain;
    b.R[o + i] += xr[i] * gain;
    if (rev) { revSend.L[o + i] += xl[i] * gain * rev; revSend.R[o + i] += xr[i] * gain * rev; }
  }
}

// Raised-cosine fade in/out on the ends of a buffer (every sound gets this: no clicks).
function fadeEnds(x, inSec, outSec) {
  const ni = Math.max(1, Math.round(inSec * SR)), no = Math.max(1, Math.round(outSec * SR));
  for (let i = 0; i < ni && i < x.length; i++) x[i] *= 0.5 - 0.5 * Math.cos((Math.PI * i) / ni);
  for (let j = 0; j < no && j < x.length; j++) x[x.length - 1 - j] *= 0.5 - 0.5 * Math.cos((Math.PI * j) / no);
  return x;
}

// ---------------------------------------------------------------------------
// Filters.
// Cytomic/TPT state-variable filter on a buffer; cutoff(i) may vary (updated every 16 samples).
function svf(x, cutoff, Q = 0.707, mode = 'lp') {
  const y = new Float32Array(x.length);
  const k = 1 / Q;
  let ic1 = 0, ic2 = 0, a1 = 0, a2 = 0, a3 = 0;
  for (let i = 0; i < x.length; i++) {
    if ((i & 15) === 0) {
      const fc = Math.min(0.45 * SR, Math.max(20, typeof cutoff === 'function' ? cutoff(i) : cutoff));
      const g = Math.tan((Math.PI * fc) / SR);
      a1 = 1 / (1 + g * (g + k)); a2 = g * a1; a3 = g * a2;
    }
    const v3 = x[i] - ic2;
    const v1 = a1 * ic1 + a2 * v3;
    const v2 = ic2 + a2 * ic1 + a3 * v3;
    ic1 = 2 * v1 - ic1; ic2 = 2 * v2 - ic2;
    y[i] = mode === 'lp' ? v2 : mode === 'bp' ? k * v1 : x[i] - k * v1 - v2;
  }
  return y;
}
// RBJ biquad (in place) for fixed master filters.
function biquad(x, type, f, Q) {
  const w = (TAU * f) / SR, c = Math.cos(w), al = Math.sin(w) / (2 * Q);
  let b0, b1, b2;
  if (type === 'lp') { b0 = (1 - c) / 2; b1 = 1 - c; b2 = b0; } else { b0 = (1 + c) / 2; b1 = -(1 + c); b2 = b0; }
  const a0 = 1 + al, a1 = -2 * c, a2 = 1 - al;
  filt(x, [b0 / a0, b1 / a0, b2 / a0], [a1 / a0, a2 / a0]);
}
function filt(x, b, a) {
  let x1 = 0, x2 = 0, y1 = 0, y2 = 0;
  for (let i = 0; i < x.length; i++) {
    const x0 = x[i];
    const y0 = b[0] * x0 + b[1] * x1 + b[2] * x2 - a[0] * y1 - a[1] * y2;
    x2 = x1; x1 = x0; y2 = y1; y1 = y0; x[i] = y0;
  }
}
const noise = (sec, rng) => { const a = new Float32Array(Math.round(sec * SR)); for (let i = 0; i < a.length; i++) a[i] = rng() * 2 - 1; return a; };
// Exponential glide between two frequencies over the buffer (for filter sweeps).
const glide = (f0, f1, sec) => (i) => f0 * (f1 / f0) ** Math.min(1, i / (sec * SR));

// ---------------------------------------------------------------------------
// Instruments. All return mono Float32Arrays starting at their onset.

// Additive voice: sum of exponentially decaying partials (recursive sine oscillators).
// Partials above ~10.5 kHz are dropped, so nothing bright reaches the top octave.
function additive(f, partials, len, att = 0.003, rel = 0.05) {
  const n = Math.round(len * SR);
  const out = new Float32Array(n);
  for (const p of partials) {
    const fr = f * p.r;
    if (fr > 10500) continue;
    const w = (TAU * fr) / SR, c = 2 * Math.cos(w);
    let y1 = -Math.sin(w), y2 = -Math.sin(2 * w);
    let e = p.a * (fr > 8000 ? (10500 - fr) / 2500 : 1);
    const dec = Math.exp(-p.d / SR);
    for (let i = 0; i < n; i++) { const y = c * y1 - y2; y2 = y1; y1 = y; out[i] += y * e; e *= dec; }
  }
  return fadeEnds(out, att, rel);
}
const marimba = (f, len = 2.0) => additive(f, [
  { r: 1, a: 1, d: 2.6 }, { r: 2, a: 0.05, d: 6 }, { r: 3.93, a: 0.3, d: 9 }, { r: 9.2, a: 0.05, d: 22 },
], len, 0.004);
const pluck = (f, len = 0.9) => additive(f, [1, 2, 3, 4, 5, 6, 7].map((k) => ({
  r: k * (1 + 0.0004 * k), a: (k === 1 ? 1 : 0.75) / k ** 1.6, d: 4.5 + 3.2 * (k - 1),
})), len, 0.003);
const bell = (f, len = 3.0, decay = 1) => additive(f, [
  { r: 1, a: 1, d: 1.3 }, { r: 2, a: 0.4, d: 1.9 }, { r: 2.76, a: 0.26, d: 2.8 }, { r: 4.07, a: 0.12, d: 4 }, { r: 5.43, a: 0.05, d: 5.5 },
].map((p) => ({ ...p, d: p.d * decay })), len, 0.002, 0.1);

// Soft kick: sine with a short downward pitch sweep (ends at ~47 Hz) and a faint click.
function kick(rng) {
  const n = Math.round(0.5 * SR), out = new Float32Array(n);
  let ph = 0, lp = 0;
  for (let i = 0; i < n; i++) {
    const t = i / SR;
    ph += (TAU * (47 + 70 * Math.exp(-t / 0.03))) / SR;
    lp += 0.25 * ((rng() * 2 - 1) - lp);
    out[i] = Math.sin(ph) * Math.exp(-t / 0.17) + 0.12 * lp * Math.exp(-t / 0.004);
  }
  return fadeEnds(out, 0.0015, 0.05);
}
// Round bass: sine + a little 2nd/3rd harmonic, gentle decay to sustain.
function bassNote(f, dur) {
  const n = Math.round((dur + 0.08) * SR), out = new Float32Array(n);
  for (let i = 0; i < n; i++) {
    const t = i / SR, p = TAU * f * t;
    out[i] = (Math.sin(p) + 0.22 * Math.sin(2 * p) + 0.06 * Math.sin(3 * p)) * (0.72 + 0.28 * Math.exp(-t / 0.15));
  }
  return fadeEnds(out, 0.008, 0.09);
}
// Shaker / soft brush: band-passed noise bursts.
const shaker = (rng) => fadeEnds(svf(noise(0.09, rng), 5600, 0.9, 'bp').map((v, i) => v * Math.exp(-i / SR / 0.022)), 0.003, 0.01);
const brush = (rng) => {
  const x = svf(noise(0.22, rng), 2400, 0.7, 'bp');
  for (let i = 0; i < x.length; i++) { const t = i / SR; x[i] = x[i] * Math.exp(-t / 0.06) + 0.35 * Math.sin(TAU * 190 * t) * Math.exp(-t / 0.035); }
  return fadeEnds(x, 0.007, 0.03);
};

// Pad voice: two detuned band-limited saws (spread L/R) + a centred sine, written straight
// into the pad bus. Release is a smooth (1-x)^shape curve that ends at exactly zero.
const rngPad = rngFor('pad');
function blepSaw(ph, dt) {
  let v = 2 * ph - 1;
  if (ph < dt) { const x = ph / dt; v -= x + x - x * x - 1; } else if (ph > 1 - dt) { const x = (ph - 1) / dt; v -= x * x + x + x + 1; }
  return v;
}
function padChord(t0, t1, notes, { att = 0.3, rel = 0.8, vel = 1, shape = 3 } = {}) {
  const g = (0.11 * vel) / Math.sqrt(notes.length);
  for (const nm of notes) {
    const f = hz(nm), n0 = Math.round(t0 * SR), nOff = Math.round(t1 * SR), nEnd = Math.min(N, nOff + Math.round(rel * SR));
    const na = att * SR, nr = rel * SR, dt1 = (f * 2 ** (-7 / 1200)) / SR, dt2 = (f * 2 ** (7 / 1200)) / SR;
    let p1 = rngPad(), p2 = rngPad(), p3 = 0;
    for (let i = n0; i < nEnd; i++) {
      const k = i - n0;
      let e = k < na ? 0.5 - 0.5 * Math.cos((Math.PI * k) / na) : 1;
      if (i >= nOff) e *= (1 - (i - nOff) / nr) ** shape;
      const s1 = blepSaw(p1, dt1), s2 = blepSaw(p2, dt2), s3 = Math.sin(TAU * p3);
      p1 += dt1; if (p1 >= 1) p1 -= 1;
      p2 += dt2; if (p2 >= 1) p2 -= 1;
      p3 += f / SR; if (p3 >= 1) p3 -= 1;
      pad.L[i] += g * e * (0.8 * s1 + 0.2 * s2 + 0.5 * s3);
      pad.R[i] += g * e * (0.2 * s1 + 0.8 * s2 + 0.5 * s3);
    }
  }
}

// ---------------------------------------------------------------------------
// Musical grid: bar 0 starts exactly at appRise; bar nBars starts exactly at outro.start.
const tRise = T(E.intro.appRise), tOutro = T(E.outro.start);
const nBars = Math.max(4, Math.round((tOutro - tRise) / 2.0));
const BAR = (tOutro - tRise) / nBars, BEAT = BAR / 4;
const barT = (b) => tRise + b * BAR;
const barOf = (t) => Math.floor((t - tRise) / BAR + 1e-6);

// Chords: a 4-bar loop I - V6 - vi - IV (D, A/C#, Bm, G); the last bar is IV | V into the outro's I.
const CH = {
  D: { bass: 'D2', pad: ['D3', 'A3', 'E4', 'F#4'], arp: ['D4', 'F#4', 'A4', 'E5'] },
  'A/C#': { bass: 'C#2', pad: ['E3', 'A3', 'C#4', 'E4'], arp: ['C#4', 'E4', 'A4', 'B4'] },
  Bm: { bass: 'B1', pad: ['D3', 'A3', 'B3', 'F#4'], arp: ['B3', 'D4', 'F#4', 'A4'] },
  G: { bass: 'G1', pad: ['D3', 'G3', 'B3', 'F#4'], arp: ['B3', 'D4', 'G4', 'A4'] },
  A: { bass: 'A1', pad: ['E3', 'A3', 'C#4', 'E4'], arp: ['C#4', 'E4', 'A4', 'B4'] },
};
const LOOP = ['D', 'A/C#', 'Bm', 'G'];
const chordAt = (b, beat) => (b === nBars - 1 ? (beat < 2 ? 'G' : 'A') : LOOP[b % 4]);

// Sections, derived from the events (not hard-coded frames).
const evs = E.events;
const firstPalette = evs.find((e) => e.kind === 'palette');
const copied = evs.find((e) => e.kind === 'copied');
const clickAfterCopy = copied && evs.find((e) => e.frame > copied.frame && e.kind === 'click');
const upB = [barOf(T(E.upload.start)), barOf(T(E.upload.end))];
const enB = [firstPalette ? barOf(T(firstPalette.frame)) + 1 : 10, clickAfterCopy ? barOf(T(clickAfterCopy.frame)) : 13];
const buildB = nBars - 2;
// energy: 0 = entering, 1 = groove, 2 = lift, -1 = calm breath after the dialog
function energy(b) {
  if (b < 2) return 0;
  if ((b >= upB[0] && b <= upB[1]) || (b >= enB[0] && b <= enB[1]) || b === buildB) return 2;
  if (b > enB[1] && b <= enB[1] + 2 && b < buildB) return -1;
  return 1;
}
const nLoops = Math.ceil(nBars / 4);
const melodyLoops = new Set([1, nLoops - 2, nLoops - 1]);

// ---------------------------------------------------------------------------
// Music.
const rngDr = rngFor('drums');
const kicks = [];
const putKick = (t, vel) => { kicks.push(t); addMono(drums, t, kick(rngDr), 0.3 * vel); };

// Intro (before bar 0): Gmaj9 pad opening slowly, then Asus4 -> A, resolving to D at appRise.
padChord(0, barT(-0.5), ['D3', 'G3', 'B3', 'F#4', 'A4'], { att: 1.4, rel: 0.6, vel: 0.85 });
padChord(barT(-0.5), barT(-0.25), ['E3', 'A3', 'D4', 'E4'], { att: 0.15, rel: 0.25, vel: 0.95 });
padChord(barT(-0.25), barT(0), ['E3', 'A3', 'C#4', 'E4'], { att: 0.08, rel: 0.5, vel: 1 });
addMono(keys, barT(-1.5), marimba(hz('G3')), 0.09, -0.2, 0.3, 0.2);
addMono(keys, barT(-1.5), marimba(hz('D4')), 0.07, 0.2, 0.3, 0.2);
addMono(keys, barT(-0.5), marimba(hz('A3')), 0.09, -0.2, 0.3, 0.2);
addMono(keys, barT(-0.5), marimba(hz('E4')), 0.07, 0.2, 0.3, 0.2);
['A4', 'B4', 'C#5', 'E5'].forEach((nm, i) => addMono(keys, barT(-0.25) + i * BEAT / 2, pluck(hz(nm)), 0.06 + 0.012 * i, -0.3 + 0.2 * i, 0.3, 0.25));
addMono(bass, barT(-0.5), fadeEnds(bassNote(hz('A1'), barT(0) - barT(-0.5)), 0.5, 0.09), 0.13);
{ // rising air into appRise
  const t0 = Math.max(0, barT(-0.7)), len = barT(0) - t0 + 0.05;
  const x = svf(noise(len, rngFor('riser')), glide(300, 2400, len), 1.2, 'bp');
  for (let i = 0; i < x.length; i++) x[i] *= (i / x.length) ** 2.5;
  addMono(tex, t0, fadeEnds(x, 0.05, 0.04), 0.25, 0, 0.4);
}

// App section, bar by bar.
const MEL = [
  'A5 - F#5 A5 B5 - A5 -',
  'E5 - - F#5 E5 - C#5 -',
  'D5 - F#5 A5 B5 - D6 -',
  'B5 - A5 - F#5 - E5 -',
].map((s) => s.split(' '));
const MEL_LAST = 'B5 - A5 - C#6 - - -'.split(' ');
const ARP8 = [0, 2, 1, 3, 2, 1, 3, 2];
const ARP16 = [0, 1, 2, 3, 2, 1, 3, 2, 0, 2, 1, 3, 2, 3, 1, 2];

for (let b = 0; b < nBars; b++) {
  const t0 = barT(b), en = energy(b), last = b === nBars - 1;
  // pad: one chord per bar (two in the last bar), slightly overlapping for legato
  if (last) {
    padChord(t0, t0 + 2 * BEAT + 0.05, CH.G.pad, { att: 0.2, rel: 0.5 });
    padChord(t0 + 2 * BEAT, t0 + 4 * BEAT + 0.02, CH.A.pad, { att: 0.15, rel: 0.35 });
  } else padChord(t0, t0 + BAR + 0.05, CH[LOOP[b % 4]].pad, { att: b === 0 ? 0.05 : 0.25, rel: 0.7 });

  // bass: long notes while the groove enters, then a gentle syncopated figure
  const hits = en === 0 ? [[0, 3.8, 1]] : last ? [[0, 1.4, 1], [1.5, 0.45, 0.6], [2, 1.95, 1]] : [[0, 1.4, 1], [1.5, 0.45, 0.6], [2, 1.4, 0.9], [3.5, 0.45, 0.6]];
  for (const [beat, len, v] of hits) addMono(bass, t0 + beat * BEAT, bassNote(hz(CH[chordAt(b, beat)].bass), len * BEAT), 0.16 * v);

  // kick (feeds the sidechain ducking), shaker, soft brush on 2 and 4
  const kb = last ? [0, 1] : en === 2 ? [0, 1, 2, 3] : en === -1 ? [0] : [0, 2];
  for (const k of kb) putKick(t0 + k * BEAT, k === 0 ? 1 : 0.8);
  if (en !== 0 || b === 1) {
    const sixteenths = en === 2 || last;
    const steps = sixteenths ? 16 : 8;
    for (let s = 0; s < steps; s++) {
      const t = t0 + (s * BAR) / steps + (rngDr() - 0.5) * 0.006;
      const off = sixteenths ? s % 4 === 2 : s % 2 === 1;
      let v = (off ? 1 : 0.55) * (sixteenths && s % 2 ? 0.6 : 1) * (en === -1 ? 0.6 : 1) * (b === 1 ? (s / steps) : 1);
      if (last) v *= 0.5 + (0.6 * s) / steps; // little crescendo into the outro
      addMono(drums, t, shaker(rngDr), 0.05 * v, s % 2 ? 0.2 : -0.15);
    }
  }
  if (en === 2) for (const k of [1, 3]) addMono(drums, t0 + k * BEAT, brush(rngDr), 0.05, 0.1, 0.25);

  // plucked arpeggio, bouncing left/right; 16ths in the lifted bars
  const ar = en === 2 ? ARP16 : ARP8;
  for (let s = 0; s < ar.length; s++) {
    const beat = (s * 4) / ar.length, nm = CH[chordAt(b, beat)].arp[ar[s]];
    const accent = s % (ar.length / 4) === 0 ? 1 : 0.72;
    const fadeIn = b === 0 ? 0.55 + (0.45 * s) / ar.length : 1;
    const v = (ar.length === 16 ? 0.8 : 1) * accent * fadeIn * (en === -1 ? 0.8 : 1);
    addMono(keys, t0 + beat * BEAT, pluck(hz(nm)), 0.07 * v, s % 2 ? 0.35 : -0.35, 0.2, 0.18);
  }

  // marimba hook (with a soft harmony a third below in the final loop)
  const loop = Math.floor(b / 4);
  if (melodyLoops.has(loop)) {
    const pat = last ? MEL_LAST : MEL[b % 4];
    pat.forEach((nm, s) => {
      if (nm === '-') return;
      const t = t0 + (s * BEAT) / 2, v = s % 2 ? 0.8 : 1;
      addMono(keys, t, marimba(hz(nm)), 0.17 * v, 0.1, 0.3, 0.32);
      if (loop === nLoops - 1) addMono(keys, t, marimba(mhz(diatonic(midi(nm), -2))), 0.07 * v, -0.25, 0.3, 0.2);
    });
  }
}

// Upload: a subtle rising band of air that tracks the progress bar.
{
  const t0 = T(E.upload.start), len = T(E.upload.end) - t0;
  const x = svf(noise(len, rngFor('upload')), glide(700, 3600, len), 1.6, 'bp');
  for (let i = 0; i < x.length; i++) { const p = i / x.length; x[i] *= (0.35 + 0.65 * p) * (0.8 + 0.2 * Math.sin(TAU * 4 * i / SR)); }
  addMono(tex, t0, fadeEnds(x, 0.4, 0.08), 0.2, 0, 0.3);
}

// Outro: final Dmaj9 rings out (pad, bass, marimba roll, bell on the melody's D6).
padChord(tOutro, tOutro + 0.9, ['D3', 'A3', 'E4', 'F#4', 'A4', 'C#5'], { att: 0.04, rel: Math.max(1, DUR - tOutro - 0.9), vel: 1.1, shape: 1.6 });
addMono(bass, tOutro, fadeEnds(bassNote(hz('D2'), DUR - tOutro - 0.1).map((v, i) => v * Math.exp(-i / SR / 1.6)), 0.008, 0.1), 0.18);
putKick(tOutro, 1);
['D4', 'F#4', 'A4', 'C#5', 'E5'].forEach((nm, i) => addMono(keys, tOutro + i * 0.035, marimba(hz(nm), 4), 0.09, -0.4 + 0.2 * i, 0.4, 0.25));
addMono(keys, tOutro, marimba(hz('D6'), 4), 0.15, 0.1, 0.4, 0.35);
addMono(keys, tOutro, bell(hz('D6'), 4.5, 0.6), 0.05, 0.1, 0.5, 0.3);

// Pad filter: keyframes [time s, cutoff Hz], log-interpolated. Opens through the intro,
// lifts with the energy, sweeps up across the theme wipe, then closes as the chord rings out.
const tw0 = T(E.wipe.start), tw1 = T(E.wipe.end);
const cutKeys = [[0, 280], [barT(-0.5), 700], [barT(0), 1500]];
for (let b = 1; b < nBars - 1; b++) cutKeys.push([barT(b) + 0.2, { 2: 2200, 1: 1700, 0: 1500, '-1': 1300 }[energy(b)]]);
cutKeys.push([barT(nBars - 1), 2000], [tw0, 2600], [tw1, 6500], [tOutro - 0.05, 2600], [tOutro + 0.3, 3200], [DUR, 700]);
const padCut = (i) => {
  const t = i / SR;
  let k = 1;
  while (k < cutKeys.length - 1 && cutKeys[k][0] < t) k++;
  const [ta, fa] = cutKeys[k - 1], [tb, fb] = cutKeys[k];
  const x = Math.min(1, Math.max(0, (t - ta) / Math.max(1e-6, tb - ta)));
  return fa * (fb / fa) ** x;
};
pad.L = svf(pad.L, padCut, 0.8);
pad.R = svf(pad.R, padCut, 0.8);
for (let i = 0; i < N; i++) { revSend.L[i] += pad.L[i] * 0.3; revSend.R[i] += pad.R[i] * 0.3; }

// Sidechain-style ducking of pad and keys under each kick.
{
  kicks.sort((a, b) => a - b);
  let k = -1;
  for (let i = 0; i < N; i++) {
    const t = i / SR;
    while (k + 1 < kicks.length && kicks[k + 1] <= t) k++;
    if (k < 0) continue;
    const x = t - kicks[k], sh = x < 0.01 ? x / 0.01 : Math.exp(-(x - 0.01) / 0.13);
    pad.L[i] *= 1 - 0.35 * sh; pad.R[i] *= 1 - 0.35 * sh;
    keys.L[i] *= 1 - 0.18 * sh; keys.R[i] *= 1 - 0.18 * sh;
  }
}

// ---------------------------------------------------------------------------
// UI sounds: frame-exact, not quantised, pitched inside D major.
const rs = rngFor('sfx');
const cents = (f, c) => f * 2 ** (c / 1200);
const tick = (sec, fc, tau) => fadeEnds(svf(noise(sec, rs), fc, 1.2, 'bp').map((v, i) => v * Math.exp(-i / SR / tau)), 0.0005, 0.004);
const mix = (...parts) => { // [buffer, gain, offsetSec]
  const n = Math.max(...parts.map(([x, , o = 0]) => x.length + Math.round(o * SR)));
  const y = new Float32Array(n);
  for (const [x, g, o = 0] of parts) { const s = Math.round(o * SR); for (let i = 0; i < x.length; i++) y[s + i] += x[i] * g; }
  return y;
};
// Sine "pop" with an exponential pitch glide.
function pop(f0, f1, glideSec, tau, len = 0.3) {
  const n = Math.round(len * SR), out = new Float32Array(n);
  let ph = 0;
  for (let i = 0; i < n; i++) { const t = i / SR; ph += (TAU * f0 * (f1 / f0) ** Math.min(1, t / glideSec)) / SR; out[i] = Math.sin(ph) * Math.exp(-t / tau); }
  return fadeEnds(out, 0.001, 0.02);
}
// Stereo noise sweep: band-pass glide f0->f1, envelope env(x in 0..1), width w(x) (1 = wide, 0 = centre).
function airSweep(len, f0, f1, Q, env, width = () => 0.6) {
  const a = noise(len, rs), b = noise(len, rs), c = noise(len, rs), n = a.length;
  const fa = svf(a, glide(f0, f1, len), Q, 'bp'), fb = svf(b, glide(f0, f1, len), Q, 'bp'), fc = svf(c, glide(f0, f1, len), Q, 'bp');
  const L = new Float32Array(n), R = new Float32Array(n);
  for (let i = 0; i < n; i++) { const x = i / n, w = width(x), e = env(x); L[i] = 3 * e * (w * fa[i] + (1 - w) * fc[i]); R[i] = 3 * e * (w * fb[i] + (1 - w) * fc[i]); }
  return [fadeEnds(L, 0.01, 0.03), fadeEnds(R, 0.01, 0.03)];
}
const pick = (arr) => arr[Math.floor(rs() * arr.length)];
const sfxPan = () => (rs() * 2 - 1) * 0.35;

const SFX = {
  click(t) {
    const f = cents(hz(pick(['A5', 'B5', 'D6', 'E6'])), (rs() * 2 - 1) * 12);
    const body = additive(f, [{ r: 1, a: 1, d: 38 }, { r: 2.01, a: 0.18, d: 70 }, { r: 3, a: 0.05, d: 120 }], 0.16, 0.0008, 0.02);
    addMono(sfx, t, mix([body, 0.2], [tick(0.012, 3400, 0.0018), 0.3]), 1, sfxPan(), 0.12);
  },
  type(t) {
    const f = cents(hz(pick(['D6', 'E6', 'F#6', 'A6'])), (rs() * 2 - 1) * 15);
    const body = additive(f, [{ r: 1, a: 1, d: 60 }, { r: 2, a: 0.1, d: 100 }], 0.1, 0.0008, 0.015);
    addMono(sfx, t, mix([body, 0.07 * (0.85 + 0.3 * rs())], [tick(0.008, 4200 + 1200 * rs(), 0.0014), 0.12]), 1, sfxPan() * 0.6, 0.1);
  },
  palette(t) {
    const [L, R] = airSweep(0.4, 500, 4500, 1.3, (x) => (x < 0.25 ? (x / 0.25) ** 2 : Math.exp(-(x - 0.25) / 0.18)));
    addStereo(sfx, t, L, R, 0.2, 0.25);
    addMono(sfx, t, pop(hz('D5'), hz('A5'), 0.05, 0.07), 0.15, 0, 0.25);
  },
  enter(t) {
    const tone = (nm) => additive(hz(nm), [{ r: 1, a: 1, d: 30 }, { r: 2, a: 0.12, d: 60 }], 0.2, 0.001, 0.02);
    addMono(sfx, t, mix([tick(0.01, 3000, 0.002), 0.25], [tone('A5'), 0.15], [tone('D6'), 0.17, 0.035]), 1, 0.05, 0.2);
  },
  drop(t) {
    const len = 0.5; // whoosh converging from wide to centre, ending on the drop
    const [L, R] = airSweep(len + 0.04, 350, 2200, 1.1, (x) => x ** 2.2, (x) => 1 - x);
    addStereo(sfx, t - len, L, R, 0.12, 0.2);
    addMono(sfx, t, pop(125, 55, 0.06, 0.1, 0.4), 0.25, 0, 0.05);
    addMono(sfx, t + 0.01, additive(hz('D5'), [{ r: 1, a: 1, d: 14 }, { r: 3.93, a: 0.2, d: 40 }], 0.4), 0.17, 0, 0.3);
  },
  done(t, name) {
    if (name === 'restored') {
      const len = 0.26; // reverse sweep: swelling, gliding down like a rewind, then a chime
      const [L, R] = airSweep(len, 5000, 1200, 1.4, (x) => 0.03 + 0.97 * x ** 2.5);
      const tone = pop(hz('A6'), hz('A5'), len, 1e9, len).map((v, i, a) => v * (i / a.length) ** 2.5);
      addStereo(sfx, t, L, R, 0.12, 0.3);
      addMono(sfx, t, fadeEnds(tone, 0.01, 0.012), 0.06, 0, 0.3);
      addMono(sfx, t + len, bell(hz('D6'), 2.5), 0.16, 0.1, 0.4, 0.15);
      addMono(sfx, t + len, bell(hz('A5'), 2.5), 0.1, -0.1, 0.4);
    } else { // success: two-note rising chime
      addMono(sfx, t, bell(hz('A5'), 2.5), 0.15, -0.12, 0.4, 0.12);
      addMono(sfx, t + 0.14, bell(hz('D6'), 2.8), 0.16, 0.12, 0.4, 0.15);
    }
  },
  copied(t) {
    addMono(sfx, t, mix([pop(hz('E6'), hz('A6'), 0.015, 0.045, 0.25), 0.2], [pop(hz('A5'), hz('A5'), 1, 0.03, 0.15), 0.08], [tick(0.006, 5000, 0.001), 0.08]), 1, 0.15, 0.25);
  },
  theme() { // filter swell across the wipe, then a low-to-high shimmer (left to right)
    const len = tw1 - tw0 + 0.35, peak = (tw1 - tw0 + 0.1) / len;
    const [L, R] = airSweep(len, 300, 6000, 1.4, (x) => (x < peak ? Math.sin((Math.PI / 2) * (x / peak)) ** 2 : Math.exp(-(x - peak) / 0.1)), () => 0.8);
    addStereo(sfx, tw0 - 0.1, L, R, 0.17, 0.35);
    ['E5', 'A5', 'B5', 'E6', 'A6'].forEach((nm, i) => addMono(sfx, tw1 + i * 0.05, bell(hz(nm), 2.2), 0.075, -0.5 + 0.25 * i, 0.6, 0.15));
  },
};

// Intro: icon pop, three ascending strata plinks, title shimmer, app-rise whoosh.
const I = E.intro;
addMono(sfx, T(I.icon), mix([pop(hz('A4'), hz('D4'), 0.025, 0.09, 0.4), 0.2], [tick(0.006, 2500, 0.0015), 0.06]), 1, 0, 0.3);
['D5', 'F#5', 'A5'].forEach((nm, i) => {
  const s = mix([marimba(hz(nm), 2.2), 1], [bell(hz(nm), 2.2, 1.6), 0.12]);
  addMono(sfx, T(I.strata[i]), s, 0.11, -0.25 + 0.25 * i, 0.4, 0.2);
});
['D6', 'A6', 'B6', 'E7'].forEach((nm, i) => { // soft, slow-attack shimmer with tremolo
  const n = Math.round(2.0 * SR);
  for (const [det, pan] of [[-4, -0.5], [4, 0.5]]) {
    const f = cents(hz(nm), det), x = new Float32Array(n);
    for (let j = 0; j < n; j++) { const t = j / SR; x[j] = Math.sin(TAU * f * t) * Math.exp(-t / 0.7) * (1 - 0.25 * (0.5 + 0.5 * Math.sin(TAU * 6 * t + i))); }
    addMono(sfx, T(I.title) + i * 0.04, fadeEnds(x, 0.12, 0.1), 0.03, pan * (0.4 + 0.15 * i), 0.6);
  }
});
{
  const pre = 0.35, len = pre + 1.4, pk = pre / len;
  const [L, R] = airSweep(len, 400, 1500, 1.0, (x) => (x < pk ? (x / pk) ** 2 : Math.exp(-(x - pk) / 0.22)), (x) => 0.3 + 0.6 * x);
  addStereo(sfx, tRise - pre, L, R, 0.15, 0.3);
}
// Outro: a reversed bell swell that lands on the final chord.
{
  const len = 0.7, x = mix([bell(hz('A5'), len), 1], [bell(hz('D6'), len), 0.8], [bell(hz('F#6'), len), 0.5]).reverse();
  for (let i = 0; i < x.length; i++) x[i] *= (i / x.length) ** 1.5;
  addMono(sfx, tOutro - len, fadeEnds(x, 0.05, 0.015), 0.1, 0, 0.5);
  const [L, R] = airSweep(len, 1500, 6000, 1.2, (x2) => x2 ** 2, () => 0.9);
  addStereo(sfx, tOutro - len, L, R, 0.05, 0.4);
}
for (const e of evs) SFX[e.kind]?.(T(e.frame), e.name);

// ---------------------------------------------------------------------------
// Space: ping-pong delay (dotted eighth) and a Freeverb-style Schroeder reverb.
function pingPong(inp, D, g, wet) {
  const yL = new Float32Array(N), yR = new Float32Array(N);
  let lpL = 0, lpR = 0;
  for (let i = D; i < N; i++) {
    lpL += 0.45 * ((inp.L[i - D] + inp.R[i - D]) * 0.5 + g * yR[i - D] - lpL);
    lpR += 0.45 * (g * yL[i - D] - lpR);
    yL[i] = lpL; yR[i] = lpR;
  }
  for (let i = 0; i < N; i++) { keys.L[i] += yL[i] * wet; keys.R[i] += yR[i] * wet; revSend.L[i] += yL[i] * wet * 0.3; revSend.R[i] += yR[i] * wet * 0.3; }
}
pingPong(dlySend, Math.round(BEAT * 0.75 * SR), 0.42, 0.5);

function freeverb(inp, room = 0.83, damp = 0.35) {
  const sc = SR / 44100, spread = 23;
  const combs = [1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617], aps = [556, 441, 341, 225];
  const pre = Math.round(0.018 * SR);
  const x = new Float32Array(N);
  // warm the input: pre-delay and a gentle low-pass, so the tail never gets bright
  for (let i = pre; i < N; i++) x[i] = (inp.L[i - pre] + inp.R[i - pre]) * 0.5;
  biquad(x, 'lp', 5500, 0.7);
  biquad(x, 'hp', 180, 0.7);
  const out = [];
  for (const off of [0, spread]) {
    const y = new Float32Array(N);
    for (const c of combs) {
      const n = Math.round((c + off) * sc), buf = new Float32Array(n);
      let idx = 0, store = 0;
      for (let i = 0; i < N; i++) {
        const o = buf[idx];
        store = o * (1 - damp) + store * damp;
        buf[idx] = x[i] * 0.015 + store * room;
        if (++idx === n) idx = 0;
        y[i] += o;
      }
    }
    for (const a of aps) {
      const n = Math.round((a + off) * sc), buf = new Float32Array(n);
      let idx = 0;
      for (let i = 0; i < N; i++) { const b = buf[idx]; buf[idx] = y[i] + b * 0.5; y[i] = b - y[i]; if (++idx === n) idx = 0; }
    }
    out.push(y);
  }
  return out;
}
const [revL, revR] = freeverb(revSend);

// ---------------------------------------------------------------------------
// Master: sum, clean the extremes, end fade, loudness normalisation, limiter, dither.
const L = new Float32Array(N), R = new Float32Array(N);
const REV = 4; // reverb return
const SFX_GAIN = 1.8; // UI sounds sit clearly above the bed
for (let i = 0; i < N; i++) {
  L[i] = pad.L[i] + bass.L[i] + drums.L[i] + keys.L[i] + tex.L[i] + sfx.L[i] * SFX_GAIN + revL[i] * REV;
  R[i] = pad.R[i] + bass.R[i] + drums.R[i] + keys.R[i] + tex.R[i] + sfx.R[i] * SFX_GAIN + revR[i] * REV;
}
for (const ch of [L, R]) {
  biquad(ch, 'hp', 32, 0.707);
  biquad(ch, 'lp', 12500, 0.54); // two stages: ~24 dB/oct above 12.5 kHz
  biquad(ch, 'lp', 12500, 1.31);
  const nf = Math.round(0.9 * SR), ni = Math.round(0.005 * SR); // natural ring-out, then a short fade to silence
  for (let j = 0; j < nf; j++) ch[N - 1 - j] *= 0.5 - 0.5 * Math.cos((Math.PI * j) / nf);
  for (let j = 0; j < ni; j++) ch[j] *= j / ni;
}

// ITU-R BS.1770-4 integrated loudness (K-weighting at 48 kHz, 400 ms blocks, gated).
function lufs(l, r) {
  const k = (x) => {
    const y = Float64Array.from(x);
    filt(y, [1.53512485958697, -2.69169618940638, 1.19839281085285], [-1.69065929318241, 0.73248077421585]);
    filt(y, [1, -2, 1], [-1.99004745483398, 0.99007225036621]);
    return y;
  };
  const kl = k(l), kr = k(r), blk = Math.round(0.4 * SR), hop = Math.round(0.1 * SR), ms = [];
  for (let s = 0; s + blk <= N; s += hop) {
    let acc = 0;
    for (let i = s; i < s + blk; i++) acc += kl[i] * kl[i] + kr[i] * kr[i];
    ms.push(acc / blk);
  }
  const L_ = (m) => -0.691 + 10 * Math.log10(m);
  const abs = ms.filter((m) => L_(m) > -70);
  const rel = L_(abs.reduce((a, b) => a + b, 0) / abs.length) - 10;
  const g = abs.filter((m) => L_(m) > rel);
  return L_(g.reduce((a, b) => a + b, 0) / g.length);
}
// True peak estimate: 4x oversampling with a windowed-sinc interpolator.
function truePeak(l, r) {
  const taps = 16, phases = [0.25, 0.5, 0.75].map((fr) => {
    const h = [];
    for (let k = -taps + 1; k <= taps; k++) { const x = k - fr; h.push((x === 0 ? 1 : Math.sin(Math.PI * x) / (Math.PI * x)) * (0.5 + 0.5 * Math.cos((Math.PI * x) / taps))); }
    const s = h.reduce((a, b) => a + b, 0);
    return h.map((v) => v / s);
  });
  let pk = 0;
  for (const x of [l, r]) for (let i = taps; i < N - taps; i++) {
    pk = Math.max(pk, Math.abs(x[i]));
    for (const h of phases) { let acc = 0; for (let k = 0; k < h.length; k++) acc += h[k] * x[i - taps + 1 + k]; pk = Math.max(pk, Math.abs(acc)); }
  }
  return 20 * Math.log10(pk);
}
// Look-ahead peak limiter: gain never exceeds what each sample needs, smooth attack/release.
const limStats = { busy: 0, minDb: 0 };
function limit(l, r, ceilDb) {
  limStats.busy = 0; limStats.minDb = 0;
  const ceil = 10 ** (ceilDb / 20), la = Math.round(0.003 * SR), rel = 1 - Math.exp(-1 / (0.12 * SR));
  const req = new Float32Array(N);
  for (let i = 0; i < N; i++) req[i] = Math.min(1, ceil / Math.max(1e-9, Math.abs(l[i]), Math.abs(r[i])));
  const h = new Float32Array(N), dq = new Int32Array(N); // running min of req over [i, i+la)
  let qh = 0, qt = 0;
  for (let j = N - 1; j >= 0; j--) {
    while (qt > qh && req[dq[qt - 1]] >= req[j]) qt--;
    dq[qt++] = j;
    while (dq[qh] >= j + la) qh++;
    h[j] = req[dq[qh]];
  }
  let g = 1, sum = 0;
  const ring = new Float32Array(la).fill(1);
  sum = la;
  const ol = new Float32Array(N), or = new Float32Array(N);
  for (let i = 0; i < N; i++) {
    g = Math.min(h[i], g + (1 - g) * rel);
    sum += g - ring[i % la]; ring[i % la] = g;
    const gg = sum / la; // box-smoothed gain (attack ramps over the look-ahead window)
    ol[i] = l[i] * gg; or[i] = r[i] * gg;
    if (gg < 0.97) limStats.busy++;
    limStats.minDb = Math.min(limStats.minDb, 20 * Math.log10(gg));
  }
  return [ol, or];
}

const TARGET = -14, TP_MAX = -1.2;
let gainDb = TARGET - lufs(L, R), ceilDb = -1.5, outL, outR, lu, tp;
for (let it = 0; it < 6; it++) {
  const g = 10 ** (gainDb / 20);
  [outL, outR] = limit(L.map((v) => v * g), R.map((v) => v * g), ceilDb);
  lu = lufs(outL, outR);
  tp = truePeak(outL, outR);
  if (Math.abs(lu - TARGET) < 0.1 && tp <= TP_MAX + 0.05) break;
  gainDb += TARGET - lu;
  if (tp > TP_MAX) ceilDb -= tp - TP_MAX + 0.05;
}

// 16-bit PCM WAV with TPDF dither.
mkdirSync(AUDIO_DIR, { recursive: true });
const rd = rngFor('dither'), wav = Buffer.alloc(44 + N * 4);
wav.write('RIFF', 0); wav.writeUInt32LE(36 + N * 4, 4); wav.write('WAVE', 8);
wav.write('fmt ', 12); wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20); wav.writeUInt16LE(2, 22);
wav.writeUInt32LE(SR, 24); wav.writeUInt32LE(SR * 4, 28); wav.writeUInt16LE(4, 32); wav.writeUInt16LE(16, 34);
wav.write('data', 36); wav.writeUInt32LE(N * 4, 40);
for (let i = 0; i < N; i++) {
  for (const [c, x] of [[0, outL], [1, outR]]) {
    const v = Math.round(x[i] * 32767 + (rd() - rd()));
    wav.writeInt16LE(Math.max(-32768, Math.min(32767, v)), 44 + i * 4 + c * 2);
  }
}
const outPath = join(AUDIO_DIR, 'score.wav');
writeFileSync(outPath, wav);

// Short report: grid, levels, and per-5 s balance of music vs UI sounds (pre-gain RMS, dBFS).
const db = (v) => (20 * Math.log10(Math.max(v, 1e-9))).toFixed(1);
const rms = (b, a, z) => { let s = 0; for (let i = a; i < z; i++) s += b.L[i] ** 2 + b.R[i] ** 2; return Math.sqrt(s / (2 * (z - a))); };
const peak = (b, a, z) => { let p = 0; for (let i = a; i < z; i++) p = Math.max(p, Math.abs(b.L[i]), Math.abs(b.R[i])); return p; };
const music = { L: new Float32Array(N), R: new Float32Array(N) };
for (let i = 0; i < N; i++) { music.L[i] = pad.L[i] + bass.L[i] + drums.L[i] + keys.L[i] + tex.L[i]; music.R[i] = pad.R[i] + bass.R[i] + drums.R[i] + keys.R[i] + tex.R[i]; }
console.log(`grid: ${nBars} bars of ${BAR.toFixed(3)} s (${(240 / BAR).toFixed(2)} BPM), bar 0 at ${tRise.toFixed(2)} s, outro at ${tOutro.toFixed(2)} s`);
console.log(`sections: upload bars ${upB}, lift bars ${enB}, build bar ${buildB}, melody loops ${[...melodyLoops]}`);
for (let s = 0; s < DUR; s += 5) {
  const a = Math.round(s * SR), z = Math.min(N, Math.round((s + 5) * SR));
  console.log(`  ${String(s).padStart(2)}-${Math.min(DUR, s + 5).toFixed(0).padStart(2)} s  music rms ${db(rms(music, a, z))}  sfx peak ${db(peak(sfx, a, z))}  music peak ${db(peak(music, a, z))}`);
}
{
  const a = Math.round(tRise * SR), z = Math.round(tOutro * SR), rv = { L: revL, R: revR };
  const byKind = {};
  for (const e of evs) {
    const s = Math.round(T(e.frame) * SR), k = e.kind + (e.name === 'restored' ? ':restored' : '');
    byKind[k] = Math.max(byKind[k] ?? 0, peak(sfx, s, Math.min(N, s + Math.round(0.5 * SR))) * SFX_GAIN);
  }
  console.log('  ui peaks (x SFX_GAIN): ' + Object.entries(byKind).map(([k, p]) => `${k} ${db(p)}`).join(', '));
  console.log('  stem rms over the app section: ' + Object.entries({ pad, bass, drums, keys, tex, sfx, rev: rv }).map(([k, b]) => `${k} ${db(rms(b, a, z))}`).join(', '));
}
console.log(`gain ${gainDb.toFixed(2)} dB, limiter ceiling ${ceilDb.toFixed(2)} dBFS -> ${lu.toFixed(2)} LUFS (limiter: max ${(-limStats.minDb).toFixed(1)} dB, >0.3 dB on ${((100 * limStats.busy) / N).toFixed(1)}% of samples), true peak ${tp.toFixed(2)} dBTP`);
console.log(`wrote ${outPath} (${DUR.toFixed(3)} s) in ${((Date.now() - started) / 1000).toFixed(1)} s`);
