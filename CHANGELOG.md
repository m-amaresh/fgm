# Changelog

All notable changes to fgm will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
