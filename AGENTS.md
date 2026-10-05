# Notes for coding agents

## Writing OpenSOP files (bases, SOPs, agents)

Read [FORMAT.md](FORMAT.md) before creating or editing any file in an OpenSOP folder. It is the complete format reference and ends with a checklist. In a project that uses OpenSOP, `opensop guide` prints the same reference.

## Working on this repo

- Go 1.22+: `go test ./...`, `go vet ./...`, `gofmt -l .` (must print nothing). Run the CLI from source with `go run ./cmd/opensop <command>`.
- `internal/model` is the source of truth for the format. `spec/*.schema.json` is the published JSON Schema; when you change a field, update `spec/` and FORMAT.md to match (a test checks the Go fields against `spec/`).
- `lock.json` block hashes are sha256 of each block's canonical JSON (`DumpJSON` in `internal/model`). Changing field order or JSON encoding changes every hash, so treat it as a breaking change.
- `tests/fixtures/restaurants/expected/` is golden output. If a rendering change is intended, regenerate it with `go run ./cmd/opensop render tests/fixtures/restaurants/sops --out tests/fixtures/restaurants/expected` and review the diff.
- `examples/livekit-restaurant/sops/` must stay valid and its `build/` current; a test checks both. Run `go run ./cmd/opensop render examples/livekit-restaurant/sops` after changing rendering.
- `skills/` holds skills shipped to users (`opensop skills install`), and FORMAT.md is printed by `opensop guide`; both are embedded in the binary (`embed.go`). Keep them in sync with the CLI's commands and output.
- Releases: push a `v*` tag; `.github/workflows/release.yml` runs GoReleaser and creates a draft release with binaries for `install.sh`.
