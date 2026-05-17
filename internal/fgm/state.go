package fgm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ── filesystem layout ──────────────────────────────────────────────

func (m *Manager) versionsDir() string  { return filepath.Join(m.root, "versions") }
func (m *Manager) downloadsDir() string { return filepath.Join(m.root, "downloads") }
func (m *Manager) binDir() string       { return filepath.Join(m.root, "bin") }
func (m *Manager) tmpDir() string       { return filepath.Join(m.root, "tmp") }

func (m *Manager) currentVersionFile() string {
	return filepath.Join(m.root, "current-version")
}

func (m *Manager) versionDir(version string) string {
	return filepath.Join(m.versionsDir(), version)
}

func (m *Manager) ensureLayout() error {
	for _, dir := range []string{m.root, m.versionsDir(), m.downloadsDir(), m.tmpDir(), m.binDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	// Clean up stale tmp entries left by previous crashed runs.
	m.cleanStaleTmp()
	return nil
}

// cleanStaleTmp removes all entries inside the tmp directory. These are
// only created during install and should not survive across invocations.
func (m *Manager) cleanStaleTmp() {
	entries, err := os.ReadDir(m.tmpDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(m.tmpDir(), e.Name()))
	}
}

// ── current version marker ────────────────────────────────────────

// markerPointsTo reports whether the current-version file names the given
// version, regardless of whether that version is still installed.
func (m *Manager) markerPointsTo(version string) bool {
	if data, err := os.ReadFile(m.currentVersionFile()); err == nil {
		if strings.TrimSpace(string(data)) == version {
			return true
		}
	}
	return false
}

func (m *Manager) deactivateCurrent() error {
	if err := removePath(m.currentVersionFile()); err != nil {
		return fmt.Errorf("remove current version file: %w", err)
	}
	return nil
}

// ── installed version queries ─────────────────────────────────────

// IsInstalled reports whether the given Go version directory exists locally.
func (m *Manager) IsInstalled(version string) bool { return m.isInstalled(version) }

func (m *Manager) isInstalled(version string) bool {
	info, err := os.Stat(m.versionDir(version))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (m *Manager) Current() (string, error) {
	data, err := os.ReadFile(m.currentVersionFile())
	if err == nil {
		version := strings.TrimSpace(string(data))
		if version == "" {
			return "", nil
		}
		if !m.isInstalled(version) {
			return "", fmt.Errorf("current version %s is not installed", version)
		}
		return version, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read current version: %w", err)
	}
	return "", nil
}

func (m *Manager) List() ([]string, string, error) {
	entries, err := os.ReadDir(m.versionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read versions dir: %w", err)
	}

	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Only include directories whose name is a valid Go version. This
		// keeps stray directories from showing up as bogus "0.0.0" entries.
		if validateVersion(entry.Name()) != nil {
			continue
		}
		versions = append(versions, entry.Name())
	}
	slices.SortFunc(versions, compareVersions)

	current, err := m.Current()
	if err != nil {
		if m.log != nil {
			m.log("warning: %v", err)
		}
		current = ""
	}
	return versions, current, nil
}

// Prune removes cached downloads and the manifest cache. It returns the
// number of files removed and total bytes freed.
func (m *Manager) Prune() (int, int64, error) {
	var removed int
	var freedBytes int64

	// Remove cached archive downloads.
	entries, err := os.ReadDir(m.downloadsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, 0, fmt.Errorf("read downloads dir: %w", err)
	}
	for _, e := range entries {
		path := filepath.Join(m.downloadsDir(), e.Name())
		if info, err := e.Info(); err == nil {
			freedBytes += info.Size()
		}
		if err := os.Remove(path); err != nil {
			return removed, freedBytes, fmt.Errorf("remove %s: %w", e.Name(), err)
		}
		removed++
	}

	// Always clear the in-memory manifest so subsequent calls re-fetch.
	m.cachedManifest = nil

	// Remove manifest cache from disk.
	if info, err := os.Stat(m.manifestCachePath()); err == nil {
		freedBytes += info.Size()
		if err := os.Remove(m.manifestCachePath()); err != nil {
			return removed, freedBytes, fmt.Errorf("remove manifest cache: %w", err)
		}
		removed++
	}

	return removed, freedBytes, nil
}
