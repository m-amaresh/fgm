package fgm

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// installShims (re)writes the go and gofmt shims. Writes are atomic so a
// shell resolving a shim mid-activation never executes a half-written script.
func (m *Manager) installShims() error {
	if err := os.MkdirAll(m.binDir(), 0o755); err != nil {
		return fmt.Errorf("create shim dir: %w", err)
	}
	for _, name := range []string{"go", "gofmt"} {
		if err := m.installShim(name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) installShim(name string) error {
	if runtime.GOOS == "windows" {
		// for /f reads the version without the trailing whitespace/CR that
		// other batch read idioms leave behind. setlocal keeps FGM_VERSION
		// from leaking into the calling shell session.
		content := "@echo off\r\n" +
			"setlocal\r\n" +
			"if \"%FGM_DIR%\"==\"\" set \"FGM_DIR=%USERPROFILE%\\.fgm\"\r\n" +
			"if not exist \"%FGM_DIR%\\current-version\" (\r\n" +
			"  echo fgm: no active Go version; run: fgm use latest 1>&2\r\n" +
			"  exit /b 1\r\n" +
			")\r\n" +
			"for /f \"usebackq tokens=*\" %%a in (\"%FGM_DIR%\\current-version\") do set \"FGM_VERSION=%%a\"\r\n" +
			fmt.Sprintf("\"%%FGM_DIR%%\\versions\\%%FGM_VERSION%%\\bin\\%s.exe\" %%*\r\n", name)
		return atomicWriteFile(filepath.Join(m.binDir(), name+".cmd"), []byte(content), 0o755)
	}

	script := "#!/bin/sh\n" +
		"set -eu\n" +
		fmt.Sprintf("FGM_DIR=${FGM_DIR:-'%s'}\n", shellEscape(m.root)) +
		"if [ ! -f \"$FGM_DIR/current-version\" ]; then\n" +
		"  echo \"fgm: no active Go version; run: fgm use latest\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"FGM_VERSION=$(tr -d '\\r\\n' < \"$FGM_DIR/current-version\")\n" +
		fmt.Sprintf("exec \"$FGM_DIR/versions/$FGM_VERSION/bin/%s\" \"$@\"\n", name)
	return atomicWriteFile(filepath.Join(m.binDir(), name), []byte(script), 0o755)
}
