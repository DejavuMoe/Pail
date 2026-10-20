# Pail brand assets

`pail-icon.svg` is the application icon: a graphite tile with three steel-blue storage strata that taper into a bucket (pail) silhouette, each ending in a small paper-white dot. It is an original design for this project, licensed under the repository's MIT license. The colors are the "Graphite & Paper" tokens described in [docs/dev/design.md](../../docs/dev/design.md).

Raster sizes (window icon PNG, Windows `.ico`, macOS `.icns`) and the Windows resource objects (`cmd/pail-desktop/rsrc_windows_{amd64,arm64}.syso`) are generated from this SVG into `generated/` and the app directory. Do not hand-edit generated files.

Regenerate everything from the repository root:

    go run ./tools/genicons
