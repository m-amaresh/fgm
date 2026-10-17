package fgm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
	// Clean up leftovers from previous crashed runs.
	m.cleanStaleTmp()
	m.cleanStalePartials()
	return nil
}

// staleAfter is how long scratch space or a partial download has to sit
// untouched before another fgm run treats it as abandoned.
//
// Nothing here is owned by a single process, so an unconditional sweep would
// let a second command delete the scratch directory an install is extracting
// into. Two fgm commands at once are rare on a single-user CLI, but the cost
// of guessing wrong is a failed install, while the cost of waiting an hour to
// reclaim a few megabytes is nothing.
const staleAfter = time.Hour

// isStale reports whether path was last modified longer ago than staleAfter.
// An unreadable entry is reported as fresh: leaving it costs disk, deleting
// something still in use costs an install.
func isStale(entry os.DirEntry) bool {
	info, err := entry.Info()
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > staleAfter
}

// cleanStaleTmp removes abandoned scratch directories. Every install removes
// its own on the way out, so anything left here belongs to a run that was
// killed.
func (m *Manager) cleanStaleTmp() {
	entries, err := os.ReadDir(m.tmpDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if isStale(e) {
			_ = os.RemoveAll(filepath.Join(m.tmpDir(), e.Name()))
		}
	}
}

// cleanStalePartials removes in-flight download temp files that a killed
// process left behind in the downloads directory.
func (m *Manager) cleanStalePartials() {
	entries, err := os.ReadDir(m.downloadsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), partialPrefix) && isStale(e) {
			_ = os.Remove(filepath.Join(m.downloadsDir(), e.Name()))
		}
	}
}

// ── current version marker ────────────────────────────────────────

// markerPointsTo reports whether the current-version file names the given
// version, regardless of whether that version is still installed — the state
// uninstall is most likely to be cleaning up. It reads the marker the same
// way Current does, so a marker Current calls active is one uninstall clears.
func (m *Manager) markerPointsTo(version string) bool {
	data, err := os.ReadFile(m.currentVersionFile())
	if err != nil {
		return false
	}
	marker, err := parseVersionFile(data, m.currentVersionFile())
	if err != nil {
		return false
	}
	return marker == version
}

func (m *Manager) deactivateCurrent() error {
	if err := os.Remove(m.currentVersionFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove current version file: %w", err)
	}
	return nil
}

// ── installed version queries ─────────────────────────────────────

// IsInstalled reports whether the given Go version directory exists locally.
func (m *Manager) IsInstalled(version string) bool { return m.isInstalled(version) }

func (m *Manager) isInstalled(version string) bool {
	// versionDir joins the version into a path, so validate before touching
	// the filesystem: a value like "../../elsewhere" would otherwise report a
	// directory outside the versions dir as an installed toolchain.
	if validateVersion(version) != nil {
		return false
	}
	info, err := os.Stat(m.versionDir(version))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (m *Manager) Current() (string, error) {
	data, err := os.ReadFile(m.currentVersionFile())
	if err == nil {
		version, err := parseVersionFile(data, m.currentVersionFile())
		if err != nil {
			return "", err
		}
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

	// Mark the version the shims would actually use from here, so a
	// .go-version pin is reflected in the listing.
	effective, err := m.EffectiveCwd()
	if err != nil {
		m.log("warning: %v", err)
		return versions, "", nil
	}
	return versions, effective.Version, nil
}

// notInstalledError reports a missing version, naming any installed versions
// from the same minor series to help with a mistyped patch number.
func (m *Manager) notInstalledError(version string) error {
	entries, err := os.ReadDir(m.versionsDir())
	if err != nil {
		return fmt.Errorf("go %s is not installed", version)
	}
	prefix := minorVersionKey(version) + "."
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasPrefix(name, prefix) && validateVersion(name) == nil {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("go %s is not installed", version)
	}
	slices.SortFunc(matches, compareVersions)
	return fmt.Errorf("go %s is not installed (installed: %s)", version, strings.Join(matches, ", "))
}

// Prune removes cached downloads, scratch space and the manifest cache. It
// returns the number of entries removed and total bytes freed.
func (m *Manager) Prune() (int, int64, error) {
	var removed int
	var freedBytes int64

	// Remove cached archive downloads.
	entries, err := os.ReadDir(m.downloadsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, 0, fmt.Errorf("read downloads dir: %w", err)
	}
	for _, e := range entries {
		// Leave in-flight downloads alone: they belong to an install that is
		// still running. Ones old enough to be abandoned are fair game.
		if strings.HasPrefix(e.Name(), partialPrefix) && !isStale(e) {
			continue
		}
		path := filepath.Join(m.downloadsDir(), e.Name())
		if info, err := e.Info(); err == nil {
			freedBytes += info.Size()
		}
		if err := os.Remove(path); err != nil {
			return removed, freedBytes, fmt.Errorf("remove %s: %w", e.Name(), err)
		}
		removed++
	}

	// Remove scratch space left by interrupted installs, again leaving alone
	// anything recent enough to belong to an install running right now.
	tmpEntries, err := os.ReadDir(m.tmpDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return removed, freedBytes, fmt.Errorf("read tmp dir: %w", err)
	}
	for _, e := range tmpEntries {
		if !isStale(e) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.tmpDir(), e.Name())); err != nil {
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
