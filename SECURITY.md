# Security Policy

## Supported versions

Only the latest release receives security fixes.

| Version | Supported |
|---------|:---------:|
| Latest release | Yes |
| Older releases | No |

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/m-amaresh/fgm/security/advisories/new)
rather than opening a public issue.

Include what you have: affected version, platform, reproduction steps, and
the impact you think it has. Expect an initial response within a week.

## What fgm trusts

fgm downloads and executes Go toolchains, so it is worth being explicit about
where trust is placed.

- **Go archives** are fetched over HTTPS from `go.dev/dl` and verified against
  the SHA-256 published in the official Go downloads manifest. The hash is
  computed while the archive streams to disk, and the file is only moved into
  the cache once it matches. A cached archive that fails verification is
  discarded and re-downloaded.
- **Archive extraction** rejects entry names that resolve outside the
  destination (absolute, UNC, drive-letter and `..` forms), skips symlink and
  hard-link entries entirely, refuses to write through a symlinked parent
  directory, normalises file modes to `0755`/`0644`, and caps total extracted
  size and entry count.
- **The fgm binary itself** is verified by the installers against the
  `checksums.txt` published with each release. Because that file is produced
  by the same workflow as the binaries, releases also carry signed build
  provenance as an independent check:

  ```sh
  gh attestation verify <archive> --repo m-amaresh/fgm
  ```

- **The `.go-version` file** is read by the shims and used to select an
  installed toolchain directory. Treat a repository's `.go-version` the same
  way you treat its build scripts.

## What is out of scope

- Vulnerabilities in the Go toolchains fgm installs — report those to the
  [Go project](https://go.dev/security/policy).
- Compromise of a machine where an attacker already has write access to
  `~/.fgm` or the shell profile.
