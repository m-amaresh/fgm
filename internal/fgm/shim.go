package fgm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LocalVersionFile is the per-directory version marker. The shims walk up
// from the working directory looking for it; the nearest one wins over the
// global current-version marker.
const LocalVersionFile = ".go-version"

// installShims (re)writes the go and gofmt shims. Writes are atomic so a
// shell resolving a shim mid-activation never executes a half-written script.
func (m *Manager) installShims() error {
	if err := os.MkdirAll(m.binDir(), 0o755); err != nil {
		return fmt.Errorf("create shim dir: %w", err)
	}
	for _, name := range shimNames {
		if err := m.installShim(name); err != nil {
			return err
		}
	}
	return nil
}

// shimNames are the tools fgm puts on PATH.
var shimNames = []string{"go", "gofmt"}

// Reshim regenerates the shims for the tools fgm manages. Upgrading fgm
// replaces the binary but leaves the shims from the previous version in
// place, so this is how a user picks up shim fixes without re-activating.
func (m *Manager) Reshim() error { return m.installShims() }

func (m *Manager) installShim(name string) error {
	return atomicWriteFile(m.shimPath(name), []byte(m.shimContent(name)), 0o755)
}

func (m *Manager) shimPath(name string) string {
	return filepath.Join(m.binDir(), shimFilename(name))
}

// shimContent is the script fgm would write for name right now. Comparing it
// against what is on disk is how doctor spots a shim left behind by an older
// fgm.
func (m *Manager) shimContent(name string) string {
	if runtime.GOOS == "windows" {
		return m.windowsShim(name)
	}
	return m.unixShim(name)
}

// shimIsCurrent reports whether the installed shim matches what this fgm
// would write. A missing shim is reported as not current.
func (m *Manager) shimIsCurrent(name string) (bool, error) {
	data, err := os.ReadFile(m.shimPath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return string(data) == m.shimContent(name), nil
}

// unixShim builds the POSIX sh shim. The directory walk uses parameter
// expansion rather than dirname so no subprocess is spawned per level, and
// the version files are read with `read` rather than $(cat) for the same
// reason — this runs on every single go invocation.
func (m *Manager) unixShim(name string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
FGM_DIR=${FGM_DIR:-'%s'}

# Reads the first line of $1 into fgm_version with the whitespace around it
# stripped, the way fgm itself reads these files. read handles the leading
# side; the loop takes the trailing side, including the CR of a CRLF file.
fgm_read_version() {
  fgm_version=""
  # EOF without a newline is valid; failure to open the file is not.
  { read -r fgm_version || :; } < "$1" || {
    echo "fgm: cannot read version file $1" >&2
    exit 1
  }
  while :; do
    case $fgm_version in
      *[[:space:]]) fgm_version=${fgm_version%%?} ;;
      *) break ;;
    esac
  done
}

# Nearest .go-version wins over the global marker.
fgm_version=""
fgm_source=""
fgm_dir=${PWD:-$(pwd)}
while :; do
  if [ -f "$fgm_dir/%s" ]; then
    fgm_source="$fgm_dir/%s"
    fgm_read_version "$fgm_source"
    break
  fi
  [ "$fgm_dir" = "/" ] && break
  fgm_dir=${fgm_dir%%/*}
  [ -z "$fgm_dir" ] && fgm_dir=/
done

if [ -z "$fgm_version" ] && [ -f "$FGM_DIR/current-version" ]; then
  fgm_source="$FGM_DIR/current-version"
  fgm_read_version "$fgm_source"
fi

if [ -z "$fgm_version" ]; then
  echo "fgm: no active Go version; run: fgm use latest" >&2
  exit 1
fi

# The version is about to become a path component, so accept nothing but an
# exact release name: a hostile .go-version must not point the shim at a
# binary outside $FGM_DIR/versions, and nothing here resolves 1.25 to a patch
# release the way fgm itself would.
fgm_valid=no
case $fgm_version in
  1.[0-9] | 1.1[0-9] | 1.20 ) fgm_valid=yes ;;
  *[!0-9.]* | *..* | .* | *. | *.*.*.* ) ;;
  *.*.* ) fgm_valid=yes ;;
esac
if [ "$fgm_valid" = no ]; then
  echo "fgm: $fgm_source does not contain a Go version (expected e.g. 1.25.5)" >&2
  exit 1
fi

fgm_bin="$FGM_DIR/versions/$fgm_version/bin/%s"
if [ ! -x "$fgm_bin" ]; then
  echo "fgm: go $fgm_version is not installed; run: fgm install $fgm_version" >&2
  exit 1
fi
exec "$fgm_bin" "$@"
`, shellEscape(m.root), LocalVersionFile, LocalVersionFile, name)
}

// windowsShim builds the batch shim. The root is baked in so the shim keeps
// working in a shell that does not export FGM_DIR, matching the Unix shim.
//
// Two cmd.exe details drive the shape of this script:
//
//   - The version files are read with `set /p`, which assigns the first line
//     verbatim. `for /f` would re-parse the value, so a .go-version holding
//     `1.2"& del x &"` would run as a command.
//   - Delayed expansion stays off in the main body, because it would eat any
//     "!" in the working directory or in the arguments passed through. The
//     one place that needs it — inspecting the untrusted version string — is
//     a subroutine that opens its own scope and hands back only a value it
//     has already reduced to digits and dots.
func (m *Manager) windowsShim(name string) string {
	return strings.ReplaceAll(fmt.Sprintf(`@echo off
setlocal
if "%%FGM_DIR%%"=="" set "FGM_DIR=%s"

rem Nearest .go-version wins over the global marker. The walk ends at the
rem drive root; the depth cap is a backstop so an unexpected path shape can
rem never spin here forever.
set "FGM_VERSION="
set "FGM_SOURCE="
set "FGM_SEARCH=%%CD%%"
set "FGM_DEPTH=0"
:fgm_find
if exist "%%FGM_SEARCH%%\%s" (
  set "FGM_SOURCE=%%FGM_SEARCH%%\%s"
  goto fgm_read
)
set /a FGM_DEPTH+=1
if %%FGM_DEPTH%% GTR 128 goto fgm_global
for %%%%p in ("%%FGM_SEARCH%%") do set "FGM_PARENT=%%%%~dpp"
if "%%FGM_PARENT%%"=="%%FGM_SEARCH%%" goto fgm_global
set "FGM_SEARCH=%%FGM_PARENT:~0,-1%%"
if "%%FGM_SEARCH:~-1%%"==":" set "FGM_SEARCH=%%FGM_SEARCH%%\"
goto fgm_find

:fgm_read
set /p "FGM_VERSION="<"%%FGM_SOURCE%%"

:fgm_global
if not defined FGM_VERSION (
  if exist "%%FGM_DIR%%\current-version" (
    set "FGM_SOURCE=%%FGM_DIR%%\current-version"
    set /p "FGM_VERSION="<"%%FGM_DIR%%\current-version"
  )
)
if not defined FGM_VERSION (
  echo fgm: no active Go version; run: fgm use latest 1>&2
  exit /b 1
)

call :fgm_clean
if errorlevel 1 (
  echo fgm: %%FGM_SOURCE%% does not contain a Go version ^(expected e.g. 1.25.5^) 1>&2
  exit /b 1
)

rem FGM_VERSION is digits and dots from here on, so expanding it is safe.
set "FGM_BIN=%%FGM_DIR%%\versions\%%FGM_VERSION%%\bin\%s.exe"
if not exist "%%FGM_BIN%%" (
  echo fgm: go %%FGM_VERSION%% is not installed; run: fgm install %%FGM_VERSION%% 1>&2
  exit /b 1
)
"%%FGM_BIN%%" %%*
exit /b %%errorlevel%%

rem Trims the spaces around FGM_VERSION and rejects anything that is not a
rem dotted number, so the version cannot escape the versions directory.
:fgm_clean
setlocal enabledelayedexpansion
set "FGM_V=!FGM_VERSION!"
:fgm_clean_lead
if defined FGM_V if "!FGM_V:~0,1!"==" " (
  set "FGM_V=!FGM_V:~1!"
  goto fgm_clean_lead
)
:fgm_clean_trail
if defined FGM_V if "!FGM_V:~-1!"==" " (
  set "FGM_V=!FGM_V:~0,-1!"
  goto fgm_clean_trail
)
if not defined FGM_V ( endlocal & exit /b 1 )
set "FGM_REST=!FGM_V!"
for %%%%c in (0 1 2 3 4 5 6 7 8 9 .) do set "FGM_REST=!FGM_REST:%%%%c=!"
if defined FGM_REST ( endlocal & exit /b 1 )
if "!FGM_V:~0,1!"=="." ( endlocal & exit /b 1 )
if "!FGM_V:~-1!"=="." ( endlocal & exit /b 1 )
if not "!FGM_V:..=!"=="!FGM_V!" ( endlocal & exit /b 1 )
rem Initial releases before Go 1.21 have exact names without a patch suffix.
for %%%%v in (1.0 1.1 1.2 1.3 1.4 1.5 1.6 1.7 1.8 1.9 1.10 1.11 1.12 1.13 1.14 1.15 1.16 1.17 1.18 1.19 1.20) do if "!FGM_V!"=="%%%%v" goto fgm_validated
rem All other release names must have exactly two dots.
set "FGM_MINOR=!FGM_V:*.=!"
if "!FGM_MINOR!"=="!FGM_V!" ( endlocal & exit /b 1 )
set "FGM_PATCH=!FGM_MINOR:*.=!"
if "!FGM_PATCH!"=="!FGM_MINOR!" ( endlocal & exit /b 1 )
if not "!FGM_PATCH:*.=!"=="!FGM_PATCH!" ( endlocal & exit /b 1 )
:fgm_validated
for /f "delims=" %%%%v in ("!FGM_V!") do (
  endlocal & set "FGM_VERSION=%%%%v"
)
exit /b 0
`, batchEscape(m.root), LocalVersionFile, LocalVersionFile, name), "\n", "\r\n")
}

// batchEscape makes a literal path safe inside a quoted batch set statement.
// Percent signs are the only character cmd.exe expands there; delayed
// expansion is off in the main body, so "!" needs no handling.
func batchEscape(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}
