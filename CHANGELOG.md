# Changelog

All notable changes to fgm will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2026-08-08

### Added

- Per-directory Go versions. A `.go-version` file pins the version for a
  directory tree; the shims walk up from the working directory and use the
  nearest one, falling back to the globally activated version when there is
  none. Switching directories switches Go, still with no shell hook.
- `fgm use <version> --local` — install if needed and pin the version for the
  current directory by writing `.go-version`, leaving the global version
  untouched. Shims are installed too, so a pin works without a prior
  `fgm use`.
- `fgm current` now reports which version is in effect and where it came from,
  e.g. `1.22.12 (pinned by /path/to/.go-version)`.
- `fgm doctor` reports the `GOTOOLCHAIN` setting, and warns when it names a
  specific toolchain — that pin silently overrides whatever version fgm
  activates. `fgm env` prints the setting and any active pin.
- `fgm reshim` regenerates the `go` and `gofmt` shims. Upgrading fgm replaces
  the binary but leaves the existing shims in place, so a shim written by an
  older release kept resolving versions the old way — ignoring `.go-version`,
  for instance — until the next `fgm use`. `fgm doctor` now reports a shim
  that is out of date, and the installers reshim on upgrade.
- Release archives now carry signed build provenance. Verify with
  `gh attestation verify <archive> --repo m-amaresh/fgm`.
- `SECURITY.md` and `CONTRIBUTING.md`.

### Changed

- Require Go 1.27.2 for development and release builds.
- `fgm list` marks the version the shims would actually use, so a
  `.go-version` pin is reflected in the listing.
- `fgm use` warns when a `.go-version` in scope overrides the version it just
  activated, instead of leaving `go version` to disagree silently.
- "not installed" errors now name the installed versions from the same minor
  series, which previously only happened on a path the CLI could not reach.
- `fgm prune` also clears scratch space left by interrupted installs.
- The shims now report `go <version> is not installed; run: fgm install
  <version>` instead of failing with a bare "No such file or directory".
- `fgm env` and `fgm doctor` report the `GOTOOLCHAIN` value Go will actually
  use, including one persisted with `go env -w`, and say where it came from.
  Only the environment variable was read before, so a machine switched to
  `local` with `go env -w` was reported as `auto (default)`. The `+auto` and
  `+path` suffixes are understood, so `local+auto` is no longer reported as a
  toolchain pin.
- `fgm uninstall` warns when a `.go-version` in scope still pins the version
  it just removed. fgm will not edit a project's file, but it can say the pin
  is now broken.
- Version markers are read the same way everywhere — first line, surrounding
  whitespace ignored, exact version required. The Unix shim used to strip
  *all* whitespace, so a `.go-version` holding `1.2 5` resolved to `1.25`; a
  marker holding `1.25` is now refused outright rather than sending you to
  `fgm install 1.25`, which installs a patch release and leaves the pin just
  as broken.

### Fixed

- Unreadable Unix version markers stop execution instead of falling back to
  the global toolchain. Files without a trailing newline remain supported.
- Exact historical initial release names (`1.0` through `1.20`) remain valid
  in markers and both shims; modern unresolved minor versions are rejected.

- **The Windows shims hung forever** whenever no `.go-version` was found in
  the directory tree — which is the ordinary case. The walk turned `C:\` into
  `C:`, whose parent is the working directory, and looped there until killed.
- Windows shims no longer assume `%USERPROFILE%\.fgm`. The real fgm root is
  baked into the generated `.cmd` files, matching the Unix shims, so a custom
  `FGM_DIR` keeps working in shells that do not export it.
- Two fgm commands running at once no longer break each other. A second
  install used to wipe the scratch directory and partial download of the one
  already running, and whichever install lost a race to the same version
  deleted the completed toolchain while cleaning up what it took to be its own
  partial install. Scratch space and partial downloads are now only swept once
  they are an hour old, and an install that arrives second keeps the toolchain
  that got there first.
- `fgm prune` no longer deletes in-flight download temp files belonging to a
  running install.

### Security

- Archive entry paths use an explicit `filepath.IsLocal` guard before joining
  the extraction destination, retaining traversal protection with a check
  recognized by current CodeQL releases.

- A `.go-version` file can no longer point the shims at a binary outside
  `~/.fgm/versions`. Both the shims and fgm itself now require an exact
  version like `1.25.5`, so a file holding `../../elsewhere` is refused
  instead of resolved — it previously became a path component in the binary
  the shim execs, which matters because `.go-version` is a file you inherit
  by cloning a repository.
- The Windows shim reads version files with `set /p`, which assigns the line
  verbatim. It used to read them with `for /f`, which re-parses what it
  assigns: a `.go-version` holding `1.2"& <command> &"` ran as a command.
  Delayed expansion is confined to the validation subroutine, so `!` in a
  working directory or in arguments passed to the toolchain survives.
- All GitHub Actions are pinned to commit SHAs rather than floating tags.
- Release workflow permissions are scoped per job; only the publishing job
  gets `contents: write`.

### Internal

- Unit tests now run on Linux, macOS and Windows in CI. Previously only Linux
  ran tests, while the other four targets were cross-compiled but never
  executed.
- The integration suite (which downloads real Go archives) runs weekly and on
  demand, instead of never running in CI.
- The generated shims are now executed for real in tests on every platform,
  covering the `.go-version` walk, an uninstalled pin, roots containing
  spaces, exit-code pass-through, and refusal of a hostile pin. The Windows
  `.cmd` shim was previously only asserted on its text, which is how it
  shipped with an infinite loop.

## [1.0.1] - 2026-06-13

### Changed

- Built with Go 1.26.4 (up from 1.26.3).
- Archives are checksum-verified while they download and only moved into the
  download cache after verification succeeds, so the cache can never hold a
  partial or corrupt archive (previously an interrupted download left a bad
  file behind until the next install rejected it).
- A cached archive that fails verification is discarded and automatically
  re-downloaded instead of aborting the install.
- The download progress message now includes the archive size.
- Removed the 10-minute overall download timeout so large downloads on slow
  connections can finish; Ctrl-C cancellation still works.
- The "version not found" error now suggests `fgm available --all`.
- `fgm current` with no active version now prints
  "No active Go version. Run: fgm use latest", matching the style and
  recovery hints of the other commands.

### Fixed

- `fgm install` / `fgm use` no longer print a duplicate
  "already installed" message to stderr.
- `go` and `gofmt` shims are written atomically, so a shell resolving a shim
  during `fgm use` can never execute a half-written script. The shim
  directory is also created if missing.
- Stale in-flight download temp files left by a killed process are swept on
  the next install.
- The Unix installer no longer creates `~/.bash_profile` when it doesn't
  exist (which would make bash login shells skip an existing `~/.profile`,
  silently dropping the user's environment); it appends to `~/.profile`
  instead.
- The Unix installer works with busybox wget (Alpine), which lacks
  `--show-progress`.
- The Windows installer warns when an existing Go installation may shadow
  fgm (machine PATH outranks user PATH on Windows), and disables progress
  rendering that slows downloads on PowerShell 5.1.

### Performance

- Fresh installs hash the archive as it streams instead of re-reading the
  whole file from disk for verification.
- The manifest fetch and archive download share one HTTP client, reusing the
  connection to go.dev.

## [1.0.0] - 2026-05-17

Initial release.

### Added

- `fgm install <version>` — install a Go toolchain. Accepts exact (`1.25.5`),
  minor (`1.25`), or `latest`. `--use` flag activates the version after install.
- `fgm use <version>` — install if needed, then activate.
- `fgm uninstall <version>` — remove an installed version.
- `fgm list` — list installed versions; marks the active one.
- `fgm current` — print the active version.
- `fgm available [--all]` — list versions available from the Go downloads
  manifest. Defaults to one entry per minor; `--all` shows every patch.
- `fgm prune` — clear the download cache.
- `fgm doctor` — health checks (directories, `PATH`, shim presence).
- `fgm env` — print resolved paths and the active version.
- `fgm version` — print build metadata.
- Cross-platform support: `linux/amd64`, `linux/arm64`, `darwin/arm64`,
  `windows/amd64`, `windows/arm64`.
- Shim-based activation: `go` and `gofmt` shims placed in `$FGM_DIR/bin`,
  resolving the active version at run time from the `current-version` marker.
- `FGM_DIR` environment variable to override the default root (`~/.fgm`).
- Global `-v` / `--verbose` flag for diagnostic output.

### Security

- SHA-256 verification of every downloaded archive against the Go downloads
  manifest. Mismatched archives are deleted.
- Zip-slip protection: archive entry names are validated to resolve within the
  extraction root, rejecting absolute paths (Unix, Windows drive, and UNC
  forms) and parent-directory escapes.
- Symlink-traversal protection: archive entries that are symlinks or hard
  links are skipped, and each extraction verifies no parent directory in the
  target path is itself a symlink.
- Extracted file modes are normalized to `0755` or `0644`, stripping
  setuid / setgid / sticky bits as defense-in-depth.
- Caps on extraction (2 GiB total, 100k entries) and downloads (512 MiB) guard
  against malformed or hostile archives.
- Atomic write for the `current-version` marker so an interrupted activation
  cannot leave a half-written file.

### Performance

- On-disk manifest cache with a 15-minute TTL avoids repeat fetches.
- Stale cache is used as an offline fallback when the manifest fetch fails.
- Downloaded archives are reused across installs of the same version.
