package fgm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const manifestURL = "https://go.dev/dl/?mode=json&include=all"

// maxExtractSize is the maximum total bytes extracted from an archive (2 GiB).
// The largest official Go archive is ~200 MB; this is a safety cap.
const maxExtractSize int64 = 2 << 30

// manifestCacheTTL is how long a cached manifest is considered fresh.
const manifestCacheTTL = 15 * time.Minute

// LogFunc is called by Manager to report progress. The CLI layer provides
// the implementation; the core package never writes to stdout/stderr directly.
type LogFunc func(format string, args ...any)

type Manager struct {
	root           string
	log            LogFunc
	Verbose        bool
	cachedManifest []releaseManifest
}

// logv logs a message only when verbose mode is enabled.
func (m *Manager) logv(format string, args ...any) {
	if m.Verbose {
		m.log("[verbose] " + fmt.Sprintf(format, args...))
	}
}

type archiveSpec struct {
	filename string
	url      string
	ext      string
	sha256   string
}

type releaseManifest struct {
	Version string        `json:"version"`
	Files   []releaseFile `json:"files"`
}

type manifestDiskCache struct {
	FetchedAt time.Time         `json:"fetched_at"`
	Releases  []releaseManifest `json:"releases"`
}

type releaseFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
}

func NewManager(root string, log LogFunc) (*Manager, error) {
	if root == "" {
		root = os.Getenv("FGM_DIR")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".fgm")
	}
	if log == nil {
		log = func(string, ...any) {} // silent by default
	}

	return &Manager{root: root, log: log}, nil
}

// Root returns the fgm home directory path.
func (m *Manager) Root() string { return m.root }

// ResolveVersion resolves "latest", minor versions (e.g. "1.25"), and exact
// versions to a concrete Go version string. The result is always a full
// version like "1.25.5".
func (m *Manager) ResolveVersion(ctx context.Context, input string) (string, error) {
	if input == "latest" {
		releases, err := m.manifest(ctx)
		if err != nil {
			return "", err
		}
		// Skip RC/beta releases — stable versions contain only digits and dots
		// after the "go" prefix.
		for _, r := range releases {
			v := strings.TrimPrefix(r.Version, "go")
			if isStableVersion(v) {
				return v, nil
			}
		}
		return "", errors.New("no stable releases found in Go manifest")
	}

	// If it looks like a full version (has two dots), validate and return.
	if strings.Count(input, ".") >= 2 {
		if err := validateVersion(input); err != nil {
			return "", err
		}
		return input, nil
	}

	// Minor version like "1.25" → find latest stable patch.
	if err := validateVersion(input); err != nil {
		return "", err
	}
	releases, err := m.manifest(ctx)
	if err != nil {
		return "", err
	}
	prefix := "go" + input
	for _, r := range releases {
		v := strings.TrimPrefix(r.Version, "go")
		if (r.Version == prefix || strings.HasPrefix(r.Version, prefix+".")) && isStableVersion(v) {
			return v, nil
		}
	}
	return "", fmt.Errorf("no release found matching %q", input)
}

// ResolveInstalledVersion resolves "latest", minor versions (e.g. "1.25"),
// and exact versions against locally installed toolchains only.
func (m *Manager) ResolveInstalledVersion(input string) (string, error) {
	if input == "latest" {
		versions, _, err := m.List()
		if err != nil {
			return "", err
		}
		if len(versions) == 0 {
			return "", errors.New("no Go versions are installed")
		}
		return versions[len(versions)-1], nil
	}

	if strings.Count(input, ".") >= 2 {
		if err := validateVersion(input); err != nil {
			return "", err
		}
		if !m.isInstalled(input) {
			return "", fmt.Errorf("go %s is not installed", input)
		}
		return input, nil
	}

	if err := validateVersion(input); err != nil {
		return "", err
	}
	versions, _, err := m.List()
	if err != nil {
		return "", err
	}
	prefix := input + "."
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i] == input || strings.HasPrefix(versions[i], prefix) {
			return versions[i], nil
		}
	}
	return "", fmt.Errorf("no installed version found matching %q", input)
}

func (m *Manager) Install(ctx context.Context, version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	m.logv("fgm root: %s", m.root)
	m.logv("platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	if m.isInstalled(version) {
		m.log("go %s is already installed", version)
		return nil
	}
	if err := m.ensureLayout(); err != nil {
		return err
	}

	m.log("Resolving go %s...", version)
	spec, err := m.resolveArchive(ctx, version)
	if err != nil {
		return err
	}
	m.logv("archive: %s", spec.filename)
	m.logv("url: %s", spec.url)
	m.logv("expected sha256: %s", spec.sha256)

	archivePath := m.archivePath(spec)
	m.logv("archive path: %s", archivePath)
	if _, err := os.Stat(archivePath); errors.Is(err, os.ErrNotExist) {
		m.log("Downloading %s...", spec.filename)
		if err := downloadFile(ctx, spec.url, archivePath); err != nil {
			_ = os.Remove(archivePath) // clean up partial download
			return err
		}
	} else if err != nil {
		return fmt.Errorf("stat archive cache: %w", err)
	} else {
		m.logv("using cached archive: %s", archivePath)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.log("Verifying checksum...")
	if err := verifyChecksum(ctx, archivePath, spec.sha256); err != nil {
		_ = os.Remove(archivePath)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.log("Extracting...")
	m.logv("extract format: %s", spec.ext)
	tmpDir, err := os.MkdirTemp(m.tmpDir(), "install-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	extractRoot := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractRoot, 0o755); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}
	if err := extractArchive(ctx, archivePath, extractRoot, spec.ext); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	extractedGo := filepath.Join(extractRoot, "go")
	if _, err := os.Stat(extractedGo); err != nil {
		return fmt.Errorf("archive missing go directory: %w", err)
	}

	target := m.versionDir(version)
	m.logv("installing to: %s", target)
	if err := moveDir(extractedGo, target); err != nil {
		_ = os.RemoveAll(target) // clean up partial version directory
		return fmt.Errorf("move installed version: %w", err)
	}
	return nil
}

func (m *Manager) Use(ctx context.Context, version string) error {
	if err := m.Install(ctx, version); err != nil {
		return err
	}
	if err := m.installShims(); err != nil {
		return err
	}
	if err := atomicWriteFile(m.currentVersionFile(), []byte(version+"\n")); err != nil {
		return fmt.Errorf("write current version: %w", err)
	}
	return nil
}

func (m *Manager) Uninstall(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if !m.isInstalled(version) {
		entries, _ := os.ReadDir(m.versionsDir())
		var matches []string
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), version+".") {
				matches = append(matches, e.Name())
			}
		}
		if len(matches) > 0 {
			return fmt.Errorf("go %s is not installed (did you mean %s?)", version, strings.Join(matches, ", "))
		}
		return fmt.Errorf("go %s is not installed", version)
	}

	current, err := m.Current()
	if err != nil {
		return err
	}
	if current == version {
		if err := m.deactivateCurrent(); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(m.versionDir(version)); err != nil {
		return fmt.Errorf("remove version: %w", err)
	}
	return nil
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

	// Backward compatibility with the initial symlink-only format.
	target, err := filepath.EvalSymlinks(m.currentLink())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			linkTarget, readErr := os.Readlink(m.currentLink())
			if errors.Is(readErr, os.ErrNotExist) {
				return "", nil
			}
			if readErr != nil {
				return "", fmt.Errorf("resolve current version: %w", err)
			}
			version := filepath.Base(linkTarget)
			return "", fmt.Errorf("current version %s is not installed", version)
		}
		return "", fmt.Errorf("resolve current version: %w", err)
	}
	version := filepath.Base(target)
	if m.isInstalled(version) {
		return version, nil
	}
	return "", fmt.Errorf("current version %s is not installed", version)
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
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	slices.SortFunc(versions, compareVersions)

	current, err := m.Current()
	if err != nil {
		return nil, "", err
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

func (m *Manager) deactivateCurrent() error {
	if err := removePath(m.currentVersionFile()); err != nil {
		return fmt.Errorf("remove current version file: %w", err)
	}
	if err := removePath(m.currentLink()); err != nil {
		return fmt.Errorf("remove legacy current path: %w", err)
	}
	return nil
}

func (m *Manager) isInstalled(version string) bool {
	info, err := os.Stat(m.versionDir(version))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (m *Manager) versionsDir() string  { return filepath.Join(m.root, "versions") }
func (m *Manager) downloadsDir() string { return filepath.Join(m.root, "downloads") }
func (m *Manager) binDir() string       { return filepath.Join(m.root, "bin") }
func (m *Manager) tmpDir() string       { return filepath.Join(m.root, "tmp") }
func (m *Manager) currentLink() string  { return filepath.Join(m.root, "current") }

func (m *Manager) currentVersionFile() string {
	return filepath.Join(m.root, "current-version")
}

func (m *Manager) versionDir(version string) string {
	return filepath.Join(m.versionsDir(), version)
}

func (m *Manager) archivePath(spec archiveSpec) string {
	return filepath.Join(m.downloadsDir(), spec.filename)
}

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
			"  echo fgm: no active Go version 1>&2\r\n" +
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
		"  echo \"fgm: no active Go version\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"FGM_VERSION=$(tr -d '\\r\\n' < \"$FGM_DIR/current-version\")\n" +
		fmt.Sprintf("exec \"$FGM_DIR/versions/$FGM_VERSION/bin/%s\" \"$@\"\n", name)
	return os.WriteFile(filepath.Join(m.binDir(), name), []byte(script), 0o755)
}

func validateVersion(version string) error {
	if version == "" {
		return errors.New("version cannot be empty")
	}
	parts := strings.Split(version, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("invalid Go version %q", version)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid Go version %q", version)
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return fmt.Errorf("invalid Go version %q", version)
			}
		}
	}
	return nil
}

// isStableVersion returns true if version contains only digits and dots
// (i.e. no "rc", "beta", etc.).
func isStableVersion(version string) bool {
	for _, r := range version {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return version != ""
}

func (m *Manager) resolveArchive(ctx context.Context, version string) (archiveSpec, error) {
	releases, err := m.manifest(ctx)
	if err != nil {
		return archiveSpec{}, err
	}

	goVersion := "go" + version
	expectedOS, expectedArch, expectedExt, err := platformReleaseParts()
	if err != nil {
		return archiveSpec{}, err
	}
	for _, release := range releases {
		if release.Version != goVersion {
			continue
		}
		for _, file := range release.Files {
			if file.Kind != "archive" {
				continue
			}
			if file.OS == expectedOS && file.Arch == expectedArch && strings.HasSuffix(file.Filename, expectedExt) {
				return archiveSpec{
					filename: file.Filename,
					url:      "https://go.dev/dl/" + file.Filename,
					ext:      expectedExt,
					sha256:   file.SHA256,
				}, nil
			}
		}
		return archiveSpec{}, fmt.Errorf("no %s/%s archive found for go %s", expectedOS, expectedArch, version)
	}

	return archiveSpec{}, fmt.Errorf("go %s was not found in the Go downloads manifest", version)
}

func (m *Manager) manifestCachePath() string {
	return filepath.Join(m.root, "manifest-cache.json")
}

func (m *Manager) manifest(ctx context.Context) ([]releaseManifest, error) {
	if m.cachedManifest != nil {
		return m.cachedManifest, nil
	}

	// Try disk cache.
	if data, err := os.ReadFile(m.manifestCachePath()); err == nil {
		var cache manifestDiskCache
		if err := json.Unmarshal(data, &cache); err == nil {
			age := time.Since(cache.FetchedAt)
			if age < manifestCacheTTL && len(cache.Releases) > 0 {
				m.logv("manifest cache hit (age: %s, %d releases)", age.Round(time.Second), len(cache.Releases))
				m.cachedManifest = cache.Releases
				return cache.Releases, nil
			}
			m.logv("manifest cache expired (age: %s)", age.Round(time.Second))
		}
	}

	m.logv("fetching manifest from %s", manifestURL)
	releases, err := fetchManifest(ctx)
	if err != nil {
		return nil, err
	}
	m.cachedManifest = releases

	// Best-effort write to disk cache.
	cache := manifestDiskCache{FetchedAt: time.Now(), Releases: releases}
	if data, err := json.Marshal(cache); err == nil {
		_ = os.MkdirAll(m.root, 0o755)
		_ = atomicWriteFile(m.manifestCachePath(), data)
	}

	return releases, nil
}
