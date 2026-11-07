import { Composition } from 'remotion'
import { HEIGHT, OUT_FPS, SUB, WIDTH } from './timeline.js'
import { T } from './data'
import { Main } from './Main'

export const Root = () => (
  <Composition id="Main" component={Main} durationInFrames={T.total * SUB} fps={OUT_FPS} width={WIDTH} height={HEIGHT} defaultProps={{ audio: true }} />
)
