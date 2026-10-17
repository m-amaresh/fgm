# Contributing to fgm

Thanks for taking the time. Issues and pull requests are both welcome.

## Getting set up

```sh
git clone https://github.com/m-amaresh/fgm.git
cd fgm
go build ./cmd/fgm
```

## Before opening a pull request

```sh
gofmt -l .              # must print nothing
go vet ./...
golangci-lint run ./...
go test -short -race ./...
```

CI runs exactly these, plus the unit tests on Linux, macOS and Windows.

## Tests

The suite is split in two:

- **Unit tests** run with `-short` and never touch the network. Everything CI
  runs on a pull request is in this set.
- **Integration tests** download real Go archives from `go.dev`. Run them with
  `go test ./...` (no `-short`). CI runs these weekly and on demand, not per
  pull request, because they are slow and depend on go.dev being reachable.

If you touch download, extraction or shim code, run the integration suite
locally before opening the pull request.

## Project layout

```
cmd/fgm/          entry point
internal/fgm/     the engine: resolve, download, verify, extract, activate
internal/cli/     cobra commands, output formatting
scripts/          install.sh, install.ps1
docs/             GitHub Pages landing page
```

`internal/fgm` never writes to stdout or stderr — it reports progress through
the `LogFunc` the CLI layer supplies. Please keep that split: anything that
formats output for a human belongs in `internal/cli`.

## Shims

The `go` and `gofmt` shims are generated in `internal/fgm/shim.go` and run on
every `go` invocation, so they need to stay fast and dependency-free.

`shim_test.go` executes the generated shim for real on whichever platform the
tests run, Windows included — the `.cmd` shim shipped with an infinite loop
once because it was only ever asserted on its text. On Windows the stand-in
for `go.exe` is a copy of the test binary, since `cmd.exe` will not exec a
script under that name. Add coverage there when changing shim behaviour, and
note that the tests only cover the platform they run on: CI is what exercises
the other one.

Two things about the shims are load-bearing and easy to undo by accident:

- The version read out of `.go-version` becomes a path component, so both
  shims validate it as a plain dotted version number before use.
- The Windows shim reads version files with `set /p` and keeps delayed
  expansion inside the validation subroutine. `for /f` re-parses what it
  assigns (a command in a `.go-version` would run), and delayed expansion in
  the main body eats `!` from paths and arguments.

## Commits and pull requests

- Keep the subject line short and imperative ("Fix Windows shim root").
- One logical change per pull request.
- Update `CHANGELOG.md` under an `## [Unreleased]` heading for anything a user
  would notice.

## Releases

Maintainers only. Releases are cut by pushing a tag:

```sh
git tag v1.2.0 && git push origin v1.2.0
```

That triggers `.github/workflows/release.yml`, which cross-compiles the five
supported targets, generates `checksums.txt`, attaches signed build
provenance, and publishes the GitHub release.
