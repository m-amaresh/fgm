# Changelog

All notable changes to fgm will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
