# Pail demo video

A 48 s demo of Pail built from frames of the real desktop app. The commands and the checks are in [docs/dev/ci.md](../../docs/dev/ci.md#demo-video).

- `cmd/pail-desktop/film_test.go` renders the app frame by frame against the in-memory demo S3 server and records the cursor and the events.
- The capture and the video are 60 fps. `src/timeline.js` is written in frames of a 30 fps film and converts: the capture's frame rate comes from `manifest.json`, the output rate is `OUT_FPS`.
- `src/timeline.js` is the only place that says when things happen: the cuts, the camera, the captions. The picture (`src/`) and the sound (`scripts/events.mjs`, `audio/synth.mjs`) both read it.
- Colors come from `cmd/pail-desktop/theme.go`; fonts are the system fonts; the score is synthesized in code.
- [Remotion](https://www.remotion.dev) is used under its free license for individuals and small companies; check [its terms](https://www.remotion.dev/docs/license) before using this in a larger organization.
- `preview.props.json` renders a silent preview: `remotion render src/index.ts Main out/preview.mp4 --scale=0.5 --props=preview.props.json`.
