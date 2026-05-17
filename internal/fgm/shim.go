package fgm

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func (m *Manager) installShims() error {
	for _, name := range []string{"go", "gofmt"} {
		if err := m.installShim(name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) installShim(name string) error {
	if runtime.GOOS == "windows" {
		// Note: "set /p" may leave trailing spaces/CR. We use a for /f loop to
		// read the version cleanly, stripping any whitespace.
		content := "@echo off\r\n" +
			"setlocal enabledelayedexpansion\r\n" +
			"if \"%FGM_DIR%\"==\"\" set \"FGM_DIR=%USERPROFILE%\\.fgm\"\r\n" +
			"if not exist \"%FGM_DIR%\\current-version\" (\r\n" +
			"  echo fgm: no active Go version; run: fgm use latest 1>&2\r\n" +
			"  exit /b 1\r\n" +
			")\r\n" +
			"for /f \"usebackq tokens=*\" %%a in (\"%FGM_DIR%\\current-version\") do set \"FGM_VERSION=%%a\"\r\n" +
			fmt.Sprintf("\"%%FGM_DIR%%\\versions\\%%FGM_VERSION%%\\bin\\%s.exe\" %%*\r\n", name)
		return os.WriteFile(filepath.Join(m.binDir(), name+".cmd"), []byte(content), 0o755)
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
	return os.WriteFile(filepath.Join(m.binDir(), name), []byte(script), 0o755)
}
