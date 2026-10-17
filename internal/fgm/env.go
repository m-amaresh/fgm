package fgm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type EnvInfo struct {
	FGMDir       string
	ShimDir      string
	VersionsDir  string
	DownloadsDir string
	// CurrentVersion is the global current-version marker.
	CurrentVersion string
	CurrentError   string
	// LocalVersion and LocalFile describe the .go-version pin in effect for
	// the working directory, if any. A pin overrides CurrentVersion.
	LocalVersion string
	LocalFile    string
	// LocalError is set when a .go-version in scope cannot be used, e.g. it
	// names something that is not a Go version.
	LocalError  string
	Platform    string
	GoToolchain string
}

func (m *Manager) Env() EnvInfo {
	info := EnvInfo{
		FGMDir:       m.root,
		ShimDir:      m.binDir(),
		VersionsDir:  m.versionsDir(),
		DownloadsDir: m.downloadsDir(),
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		GoToolchain:  goToolchain().String(),
	}

	if dir, err := os.Getwd(); err == nil {
		version, path, err := FindLocalVersion(dir)
		switch {
		case err != nil:
			info.LocalError = err.Error()
		case version != "":
			info.LocalVersion = version
			info.LocalFile = path
		}
	}

	current, err := m.Current()
	if err != nil {
		info.CurrentError = err.Error()
		return info
	}
	info.CurrentVersion = current
	return info
}

// toolchainSetting is the GOTOOLCHAIN value Go will actually use, and where
// it was set.
type toolchainSetting struct {
	Value string
	// Source names where the value came from, and is empty when nothing set
	// it and Go's default applies.
	Source string
}

const (
	toolchainFromEnv  = "environment"
	toolchainFromFile = "go env -w"
)

// String renders the setting the way fgm reports it, always naming where the
// value came from so it is clear whether editing the environment will change
// anything.
func (t toolchainSetting) String() string {
	if t.Source == "" {
		return t.Value + " (default)"
	}
	return t.Value + " (from " + t.Source + ")"
}

// IsPin reports whether the setting names a specific toolchain, which silently
// overrides the version fgm activates.
//
// Go's syntax allows a "+auto" or "+path" suffix on the name, so local+auto
// says "use the installed toolchain, and switch if a go.mod demands newer" —
// that is the default behaviour, not a pin. A named base like go1.25.0+auto
// still is one: it sets a minimum other than what fgm activated.
func (t toolchainSetting) IsPin() bool {
	base, _, _ := strings.Cut(t.Value, "+")
	switch base {
	case "auto", "local", "path":
		return false
	}
	return true
}

// goToolchain resolves GOTOOLCHAIN the way cmd/go does: the environment wins,
// then the file `go env -w` persists settings to, then the default. Reading
// only the environment reports "auto" on a machine that has been switched to
// "local" with `go env -w`, which is exactly the case worth catching.
func goToolchain() toolchainSetting {
	if v := os.Getenv("GOTOOLCHAIN"); v != "" {
		return toolchainSetting{Value: v, Source: toolchainFromEnv}
	}
	if v := goEnvFileValue("GOTOOLCHAIN"); v != "" {
		return toolchainSetting{Value: v, Source: toolchainFromFile}
	}
	return toolchainSetting{Value: "auto"}
}

// goEnvFileValue reads a key from the Go environment file. The location
// matches cmd/go: $GOENV when set, the user config dir otherwise, and the
// file is skipped entirely when GOENV is "off".
func goEnvFileValue(key string) string {
	path := os.Getenv("GOENV")
	switch path {
	case "off":
		return ""
	case "":
		dir, err := os.UserConfigDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(dir, "go", "env")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(data)) {
		name, value, found := strings.Cut(line, "=")
		if found && name == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
