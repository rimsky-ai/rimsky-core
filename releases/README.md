# Release notes

This directory holds one Markdown file per release tag,
`releases/vX.Y.Z.md`. Each file is written by the `/release` skill
when a formal release is cut and is also attached to the matching
GitHub Release.

See `../RELEASING.md` for the release process — what `/release`
does, what `make dev-release` does, what gets tagged where, and the
release-notes template the skill fills in.

Dev/nightly releases (`v0.X.0-dev.YYYYMMDD.gSHA` tags) do not produce
files here; they ship without notes by design.
