package fgm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VersionSource is a resolved Go version together with where it came from.
// Path names the .go-version file that supplied it, and is empty when the
// version came from the global current-version marker.
type VersionSource struct {
	Version string
	Path    string
}

// IsLocal reports whether the version came from a .go-version file.
func (s VersionSource) IsLocal() bool { return s.Path != "" }

// FindLocalVersion walks up from dir looking for a .go-version file and
// returns the version it names along with the path it was read from.
//
// The walk stops at the first .go-version it finds, even when that file is
// empty — an empty marker yields an empty version and lets the caller fall
// back to the global one rather than continuing up to a parent file. The
// shims implement exactly the same rule.
//
// A file naming something that is not a Go version is an error rather than a
// silently ignored pin: the shims refuse it too, so reporting it is the only
// way `fgm current` and `go version` agree.
func FindLocalVersion(dir string) (version, path string, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	for {
		candidate := filepath.Join(dir, LocalVersionFile)
		data, readErr := os.ReadFile(candidate)
		if readErr == nil {
			version, err := parseVersionFile(data, candidate)
			if err != nil {
				return "", "", err
			}
			return version, candidate, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return "", "", fmt.Errorf("read %s: %w", candidate, readErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", nil
		}
		dir = parent
	}
}

// parseVersionFile reads a version marker exactly the way the shims do: the
// first line, with the whitespace around it stripped. An empty marker yields
// an empty version; anything else has to be an exact version, because the
// shims paste it straight into the path of the binary they exec and resolve
// nothing.
func parseVersionFile(data []byte, path string) (string, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	version := strings.TrimSpace(line)
	if version == "" {
		return "", nil
	}
	if err := validateMarkerVersion(version); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return version, nil
}

// Effective resolves the Go version the shims would use when invoked from
// dir. A .go-version file takes precedence over the global marker. It
// returns an error when the resolved version is not installed, mirroring
// Current.
func (m *Manager) Effective(dir string) (VersionSource, error) {
	version, path, err := FindLocalVersion(dir)
	if err != nil {
		return VersionSource{}, err
	}
	if version != "" {
		if !m.isInstalled(version) {
			return VersionSource{}, fmt.Errorf("go %s (from %s) is not installed", version, path)
		}
		return VersionSource{Version: version, Path: path}, nil
	}

	global, err := m.Current()
	if err != nil {
		return VersionSource{}, err
	}
	return VersionSource{Version: global}, nil
}

// EffectiveCwd resolves the effective version from the working directory.
func (m *Manager) EffectiveCwd() (VersionSource, error) {
	dir, err := os.Getwd()
	if err != nil {
		return VersionSource{}, fmt.Errorf("resolve working directory: %w", err)
	}
	return m.Effective(dir)
}

// UseLocal installs the version if needed and pins it for dir by writing a
// .go-version file, leaving the global marker untouched. Shims are installed
// too, so pinning works without a prior global activation.
func (m *Manager) UseLocal(ctx context.Context, version, dir string) (string, error) {
	// Never write a marker fgm would then refuse to read.
	if err := validateMarkerVersion(version); err != nil {
		return "", err
	}
	if err := m.Install(ctx, version); err != nil {
		return "", err
	}
	if err := m.installShims(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, LocalVersionFile)
	if err := atomicWriteFile(path, []byte(version+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}
