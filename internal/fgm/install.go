package fgm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type archiveSpec struct {
	filename string
	url      string
	ext      string
	sha256   string
}

func (m *Manager) archivePath(spec archiveSpec) string {
	return filepath.Join(m.downloadsDir(), spec.filename)
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
	return m.Activate(version)
}

// Activate points the current-version marker at an already-installed Go version
// and refreshes the shims. It returns an error if the version is not installed.
func (m *Manager) Activate(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if !m.isInstalled(version) {
		return fmt.Errorf("go %s is not installed", version)
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

	// Compare raw marker contents instead of routing through Current(), which
	// errors when the marker points to a missing version — exactly the broken
	// state uninstall is most likely to be cleaning up.
	if m.markerPointsTo(version) {
		if err := m.deactivateCurrent(); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(m.versionDir(version)); err != nil {
		return fmt.Errorf("remove version: %w", err)
	}
	return nil
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
			if file.Kind == "archive" && file.OS == expectedOS && file.Arch == expectedArch && strings.HasSuffix(file.Filename, expectedExt) {
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
