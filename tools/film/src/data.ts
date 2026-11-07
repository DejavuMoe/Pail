import manifestJson from '../public/capture/manifest.json'
import { buildTimeline } from './timeline.js'

export type Manifest = {
  fps: number
  width: number
  height: number
  logicalWidth: number
  logicalHeight: number
  frames: string[]
  cursor: number[][]
  events: { frame: number; kind: string; name?: string; x?: number; y?: number }[]
  scenes: Record<string, [number, number]>
}

export const manifest = manifestJson as unknown as Manifest
export const T = buildTimeline(manifest)
