<p align="center">
  <strong>fgm</strong> — Fast Go Manager
  <br>
  <em>Install and switch Go versions in seconds. No shell hooks, no magic.</em>
</p>

<p align="center">
  <a href="#install">Install</a> &nbsp;&bull;&nbsp;
  <a href="#quick-start">Quick start</a> &nbsp;&bull;&nbsp;
  <a href="#commands">Commands</a> &nbsp;&bull;&nbsp;
  <a href="#how-it-works">How it works</a> &nbsp;&bull;&nbsp;
  <a href="#supported-platforms">Platforms</a>
</p>

---

## Install

**Linux / macOS**

```sh
curl -fsSL https://raw.githubusercontent.com/m-amaresh/fgm/main/scripts/install.sh | bash
```

**Windows (PowerShell)**

```powershell
iwr -useb https://raw.githubusercontent.com/m-amaresh/fgm/main/scripts/install.ps1 | iex
```

The installer downloads the correct binary for your OS and architecture, places it on your `PATH`, and sets up `~/.fgm/bin` for the Go shims.

> **Linux / macOS:** if you previously installed Go via the official installer (`/usr/local/go`), fgm will still take priority since `~/.fgm/bin` is prepended to `PATH`. You can safely remove the old installation with `sudo rm -rf /usr/local/go`.

> **Windows:** a system-wide Go installation (on the machine `PATH`) takes priority over fgm's user `PATH` entries. Uninstall it, or remove it from the system `PATH`, to let fgm manage Go. The installer warns when it detects this.

> Restart your terminal after installation, or `source` your shell profile as shown in the installer output.

Both installers verify the downloaded archive against the release
`checksums.txt`. Release archives also carry signed build provenance, which
you can verify independently:

```sh
gh attestation verify fgm_v1.1.0_linux_amd64.tar.gz --repo m-amaresh/fgm
```

## Quick start

```sh
fgm install latest    # download the latest stable Go release
fgm use latest        # install (if needed) + activate
go version            # verify it worked
```

## Commands

| Command | Alias | Description |
|---------|-------|-------------|
| `fgm install <version>` | `in` | Download a Go version. Add `--use` to activate it in one step. |
| `fgm use <version>` | | Install if needed, then activate |
| `fgm use <version> --local` | | Pin the version for this directory via `.go-version` |
| `fgm uninstall <version>` | `un` | Remove an installed Go version |
| `fgm list` | `ls` | List installed versions |
| `fgm available` | `list-remote` | List installable Go versions |
| `fgm current` | | Show the active Go version |
| `fgm prune` | | Remove cached downloads and the manifest cache |
| `fgm reshim` | | Regenerate the `go` and `gofmt` shims (run after upgrading fgm) |
| `fgm env` | | Print fgm environment diagnostics |
| `fgm doctor` | | Check fgm installation health |
| `fgm version` | | Print fgm version, commit, and build info |
| `fgm completion <shell>` | | Generate shell completion script (bash, zsh, fish, powershell) |
| `fgm --help` | | Show help |
| `fgm -v <command>` | | Run any command with verbose diagnostics |

### Version formats

| Input | Resolves to |
|-------|-------------|
| `1.25.5` | Exact version |
| `1.25` | Latest 1.25.x patch |
| `latest` | Latest stable release |

### Examples

```sh
fgm install 1.22          # install latest Go 1.22.x
fgm use 1.23.4            # install + activate exact version
fgm install latest         # grab the newest stable release

fgm list                   # see what's installed
#   1.22.12
# * 1.23.4 (current)
#   1.24.1

fgm uninstall 1.22.12     # remove a version you no longer need
```

## Per-directory versions

A project can pin its own Go version with a `.go-version` file. The shims read
it at invocation time, so switching directories switches Go — still with no
shell hook.

```sh
cd ~/work/legacy-service
fgm use 1.22.12 --local    # installs if needed, writes ./.go-version

go version                 # go version go1.22.12 ...
cd ~ && go version         # back to the global version
```

The shims walk up from the working directory and use the **nearest**
`.go-version` they find, falling back to the globally activated version when
there is none. Commit the file to share the pin with your team.

```
~/work/legacy-service/.go-version     ← "1.22.12"
~/work/legacy-service/cmd/api/        ← also gets 1.22.12
~/work/other-project/                 ← global version
```

`fgm current` reports which one is in effect and where it came from:

```sh
$ fgm current
1.22.12 (pinned by /home/you/work/legacy-service/.go-version)
```

> The pinned version must be an exact version (`1.22.12`, not `1.22`) and must
> already be installed — the shims deliberately do no network or resolution
> work. `fgm use <version> --local` handles both.

The file has to contain nothing but an exact version: the shims turn it
straight into a path under `~/.fgm/versions` and resolve nothing, so `1.22`,
a path, or a comment on the first line is refused rather than interpreted.
Only the first line is read, and the whitespace around it is ignored. The
same rule applies to `~/.fgm/current-version`.

Historical initial releases through Go 1.20 use exact names such as `1.19`
and `1.20` (without `.0`); these are accepted as literal installed versions.

## Go's own toolchain switching

Go 1.21 and later can download and run a *different* toolchain than the one
you invoked. If a project's `go.mod` requires a newer Go than the active
version, `go build` fetches that toolchain and uses it, so `go version` can
disagree with `fgm current`. This is Go's behaviour, not fgm's.

```sh
go env GOTOOLCHAIN     # auto (default) — Go may switch
export GOTOOLCHAIN=local  # never switch; fail instead if go.mod needs newer
```

`fgm doctor` reports the effective setting and where it came from — the
environment or `go env -w`, which writes to a file rather than the environment
— and warns if `GOTOOLCHAIN` names a specific toolchain, since that pin
overrides whatever version fgm activates.

## How it works

1. Downloads official binaries from [go.dev/dl](https://go.dev/dl/) and verifies SHA-256 checksums.
2. Extracts each verified version into `~/.fgm/versions/<version>/`.
3. `fgm use` writes the active version to `~/.fgm/current-version` and generates lightweight shims in `~/.fgm/bin/`.
4. The `go` and `gofmt` shims resolve the version at invocation time — nearest
   `.go-version` first, then `current-version` — **no shell hook or eval needed**.
5. The Go release manifest is cached locally for 15 minutes so repeated commands stay fast.

### Directory layout

> On all platforms the default location is `~/.fgm`. Override with the `FGM_DIR` environment variable.

```
~/.fgm/
  bin/                 ← go & gofmt shims (add to PATH once)
  current-version      ← plain text file with the active version
  versions/
    1.22.12/
    1.23.4/
    1.24.1/
  downloads/           ← cached archive tarballs/zips
  tmp/                 ← scratch space during installs (cleaned automatically)
  manifest-cache.json  ← cached release manifest
```

## Supported platforms

| OS | amd64 | arm64 |
|---------|:-----:|:-----:|
| Linux | Yes | Yes |
| macOS | No | Yes |
| Windows | Yes | Yes |

macOS support is Apple Silicon only (`darwin/arm64`).

## Build from source

```sh
git clone https://github.com/m-amaresh/fgm.git
cd fgm
go build -o fgm ./cmd/fgm
```

To embed version info:

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w \
    -X github.com/m-amaresh/fgm/internal/cli.Version=v1.0.0 \
    -X github.com/m-amaresh/fgm/internal/cli.Commit=$(git rev-parse --short HEAD) \
    -X github.com/m-amaresh/fgm/internal/cli.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o fgm ./cmd/fgm
```

## Versioning

fgm uses **semantic versioning**. See [CHANGELOG.md](CHANGELOG.md) for release notes.

| Format | Example | Meaning |
|--------|---------|---------|
| `vMAJOR.MINOR.PATCH` | `v1.0.0` | A tagged release |
| `vMAJOR.MINOR.PATCH-PRERELEASE` | `v1.1.0-rc.1` | A prerelease build |

```
v1.0.0       ← first stable release
v1.0.1       ← patch release
v1.1.0       ← minor release
v2.0.0       ← major release
v1.1.0-rc.1  ← prerelease
```

To create a release, push a tag matching this format:

```sh
git tag v1.0.0 && git push origin v1.0.0
```

## Uninstall fgm

To completely remove fgm and all installed Go versions from your system:

### Linux / macOS

**1. Remove fgm data directory** (all Go versions, shims, cache):

```sh
rm -rf ~/.fgm
```

**2. Remove the fgm binary:**

```sh
rm -f ~/.local/bin/fgm
```

**3. Remove the PATH entry from your shell profile.**

Open your shell config file and delete the line containing `# fgm-managed-path`:

| Shell | File |
|-------|------|
| bash | `~/.bashrc`, plus `~/.bash_profile` or `~/.profile` |
| zsh | `~/.zshrc` |
| fish | `~/.config/fish/config.fish` |
| other | `~/.profile` |

The line looks like:

```sh
export PATH="${HOME}/.local/bin:${HOME}/.fgm/bin:${HOME}/go/bin:$PATH" # fgm-managed-path
```

**4. Restart your terminal** or `source` your shell profile.

### Windows (PowerShell)

**1. Remove fgm data directory** (all Go versions, shims, cache):

```powershell
Remove-Item -Recurse -Force "$env:USERPROFILE\.fgm"
```

**2. Remove fgm from your PATH.**

Open **Settings > System > About > Advanced system settings > Environment Variables**, find `PATH` under **User variables**, and remove these entries:

- `C:\Users\<you>\.fgm\bin`
- `C:\Users\<you>\go\bin`

Or via PowerShell:

```powershell
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
$cleaned = ($userPath -split ';' | Where-Object {
    $_ -notmatch 'fgm'
}) -join ';'
[Environment]::SetEnvironmentVariable("PATH", $cleaned, "User")
```

> Restart your terminal after running this. The `go\bin` PATH entry is for your Go binaries (e.g. `go install` tools) — remove it manually if you no longer use Go at all.

**3. Restart your terminal.**

## License

MIT
