import { Composition } from 'remotion'
import { FPS, HEIGHT, WIDTH } from './timeline.js'
import { T } from './data'
import { Main } from './Main'

export const Root = () => (
  <Composition id="Main" component={Main} durationInFrames={T.total} fps={FPS} width={WIDTH} height={HEIGHT} defaultProps={{ audio: true }} />
)
