import { Config } from '@remotion/cli/config'

Config.setVideoImageFormat('jpeg')
Config.setJpegQuality(95)
Config.setOverwriteOutput(true)
// A system Chromium (the WSL runner has one) avoids downloading a browser.
if (process.env.PAIL_FILM_CHROMIUM) {
  Config.setBrowserExecutable(process.env.PAIL_FILM_CHROMIUM)
}
Config.setChromiumOpenGlRenderer('angle')
