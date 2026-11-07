import { AbsoluteFill, Audio, Sequence, staticFile, useCurrentFrame } from 'remotion'
import { C, clamp, easeInOut, easeOut } from './theme'
import { APP_START, OUTRO_FRAMES } from './timeline.js'
import { camAt } from './timeline.js'
import { T, manifest } from './data'
import { Stage } from './components/Stage'
import { Cursor, Clicks, FileCards } from './components/Overlays'
import { Intro } from './components/Intro'
import { Outro } from './components/Outro'
import { Captions, Honest } from './components/Captions'

const WIPE_FRAMES = 24

export const Main = ({ audio = true }: { audio?: boolean }) => {
  const v = useCurrentFrame()
  const c = T.vidToCap(v)
  const ci = Math.min(manifest.frames.length - 1, Math.max(0, Math.floor(c)))
  const pose = camAt(T.cam, c)
  const enter = easeOut((v - APP_START) / 40)
  const exit = easeInOut((v - T.outroStart) / 30)

  const theme = T.a.theme
  const wipeStart = T.capToVid(theme)
  const wp = easeInOut((v - wipeStart) / WIPE_FRAMES)
  const darkNow = c >= theme
  const showLight = !(darkNow && wp >= 1)
  const lightFile = manifest.frames[Math.min(ci, theme - 1)]
  const R = wp * 1500
  const clip = `circle(${R}px at 960px 520px)`

  const overlays = (
    <>
      {c > T.a.drop - 90 && c < T.a.drop + 12 && <FileCards c={c} />}
      <Clicks c={c} z={pose.z} />
      <Cursor c={c} z={pose.z} />
    </>
  )

  return (
    <AbsoluteFill style={{ background: C.graphite }}>
      {audio && <Audio src={staticFile('audio/score.wav')} />}
      {v < APP_START + 45 && <Intro />}
      {v >= APP_START - 1 && showLight && (
        <Stage file={lightFile} backdrop={C.stageLight} pose={pose} enter={enter} exit={exit}>
          {!darkNow && overlays}
        </Stage>
      )}
      {darkNow && v < T.outroStart + 35 && (
        <Stage file={manifest.frames[ci]} backdrop={C.stageDark} pose={pose} enter={1} exit={exit} clip={wp >= 1 ? undefined : clip}>
          {overlays}
        </Stage>
      )}
      <Honest v={v} from={APP_START + 40} to={T.outroStart + 6} dark={darkNow && wp > 0.5} />
      <Captions v={v} />
      <Sequence from={T.outroStart} durationInFrames={OUTRO_FRAMES}>
        <Outro />
      </Sequence>
    </AbsoluteFill>
  )
}
