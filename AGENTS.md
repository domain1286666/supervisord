# Repository Guidelines

## Project Structure & Module Organization

The root `main` package implements daemon startup, CLI commands, RPC endpoints, and platform adapters. Core packages include `process/`, `config/`, `events/`, `logger/`, and `signals/`; `pidproxy/` contains a helper executable. `xmlrpcclient/` is a separate Go module included by `go.work`. Web assets live in `webgui/` and are embedded through `assets.go`. Tests sit beside implementation files as `*_test.go`.

## Build, Test, and Development Commands

Use Go 1.27.0 or newer, matching the module and workspace declarations; CI uses Go 1.27. Run commands from the repository root:

- `go mod download`: download dependencies.
- `go build ./...`: compile the root module's packages and commands.
- `go build -tags release -o supervisord .`: build the main binary; use `supervisord.exe` on Windows.
- `go run . -c path/to/local.conf`: run in the foreground with an explicit configuration.
- `go test -v ./... ./xmlrpcclient/...`: test both workspace modules.
- `go vet ./... ./xmlrpcclient/...`: run static checks.

## Coding Style & Naming Conventions

Use `gofmt` and tabs for Go indentation. Keep package names lowercase, exported identifiers in `MixedCaps`, and internal identifiers in `mixedCaps`. Follow nearby filename conventions, including `_linux.go` and `_windows.go` for platform code. Preserve platform build constraints. Keep changes focused and document non-obvious behavior.

## Testing Guidelines

Use Go's standard `testing` package, `*_test.go` files, and `TestXxx` functions. Add regression tests for changed behavior. Use `t.TempDir()` for fixtures and stop child processes during cleanup. Run the full suite on Linux: several process tests require `/bin/sh`. No numeric coverage threshold is configured. For focused runs, use `go test ./config -run TestProgramConfig`.

## Commit & Pull Request Guidelines

History mixes imperative subjects with Conventional Commits such as `fix(process): ...` and `test(config): ...`. Prefer concise imperative subjects, adding a scope when helpful. Keep PRs focused; describe behavior changes, link relevant issues, and list validation commands and results. Include screenshots for web UI changes. Update `README.md` when CLI options or configuration behavior changes.

## Configuration Tips

Use `supervisor.ini` as a reference; replace its sample Python commands before running. Keep credentials and runtime logs out of commits.
