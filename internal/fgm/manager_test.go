package fgm

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closeAll closes a list of io.Closers, failing the test on any error.
func closeAll(t *testing.T, closers ...io.Closer) {
	t.Helper()
	for _, c := range closers {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// ── validateVersion ─────────────────────────────────────────────────

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"1.25.5", false},
		{"1.25", false},
		{"1", true},
		{"1.2.3.4", true},
		{"", true},
		{"1.25.5-rc1", true},
		{"abc", true},
		{"1..2", true},
		{"../etc", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVersion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

// ── isStableVersion ─────────────────────────────────────────────────

func TestIsStableVersion(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1.25.5", true},
		{"1.25", true},
		{"1.25rc1", false},
		{"1.25beta1", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isStableVersion(tt.input); got != tt.want {
				t.Errorf("isStableVersion(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ── sanitizePath ────────────────────────────────────────────────────

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		entry   string
		wantErr bool
	}{
		{"normal path", "/tmp/extract", "go/bin/go", false},
		{"traversal blocked", "/tmp/extract", "../../../etc/passwd", true},
		{"dot-dot in middle", "/tmp/extract", "go/../../etc/passwd", true},
		{"clean path", "/tmp/extract", "go/bin/../bin/go", false},
		{"base itself", "/tmp/extract", ".", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sanitizePath(tt.base, tt.entry)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizePath(%q, %q) error = %v, wantErr %v", tt.base, tt.entry, err, tt.wantErr)
			}
			if err == nil && !strings.HasPrefix(result, filepath.Clean(tt.base)) {
				t.Errorf("sanitizePath(%q, %q) = %q, escapes base", tt.base, tt.entry, result)
			}
		})
	}
}

// ── ensureNoSymlinkParent ───────────────────────────────────────────

func TestEnsureNoSymlinkParent(t *testing.T) {
	root := t.TempDir()

	// Create: root/a/b/file (normal dirs)
	normalDir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(normalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(normalDir, "file")
	if err := ensureNoSymlinkParent(root, target); err != nil {
		t.Errorf("expected no error for normal dirs, got: %v", err)
	}

	// Create: root/c -> /tmp (symlink)
	symlinkDir := filepath.Join(root, "c")
	if err := os.Symlink("/tmp", symlinkDir); err != nil {
		t.Fatal(err)
	}
	target2 := filepath.Join(symlinkDir, "evil")
	if err := ensureNoSymlinkParent(root, target2); err == nil {
		t.Error("expected error for symlink parent, got nil")
	}
}

// ── extractTarGz: symlink entries are skipped ──────────────────────

func TestExtractTarGz_SkipsSymlinks(t *testing.T) {
	// Build a tar.gz with a symlink entry followed by a file that would
	// escape through it. Since symlinks are skipped, the file lands safely
	// inside the destination and /tmp/pwned is never created.
	archivePath := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Entry 1: go/evil -> /tmp (symlink — will be skipped)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     "go/evil",
		Linkname: "/tmp",
	}); err != nil {
		t.Fatal(err)
	}

	// Entry 2: go/evil/pwned (would write to /tmp/pwned if symlink existed)
	content := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "go/evil/pwned",
		Size:     int64(len(content)),
		Mode:     0o644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}

	closeAll(t, tw, gw, f)

	dest := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archivePath, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The symlink was skipped, so go/evil is a regular directory and the
	// file landed safely inside dest.
	if _, err := os.Stat("/tmp/pwned"); err == nil {
		_ = os.Remove("/tmp/pwned")
		t.Fatal("malicious file was written to /tmp/pwned")
	}
}

// ── extractTarGz: normal archive ────────────────────────────────────

func TestExtractTarGz_NormalArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "good.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Directory
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     "go/bin/",
		Mode:     0o755,
	}); err != nil {
		t.Fatal(err)
	}

	// Regular file
	content := []byte("#!/bin/sh\necho hello")
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "go/bin/go",
		Size:     int64(len(content)),
		Mode:     0o755,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}

	closeAll(t, tw, gw, f)

	dest := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archivePath, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "go", "bin", "go"))
	if err != nil {
		t.Fatalf("file not extracted: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q", got)
	}
}

// ── extractZip: normal archive ──────────────────────────────────────

func TestExtractZip_NormalArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "good.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	w, err := zw.Create("go/bin/go")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("binary content")
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}

	closeAll(t, zw, f)

	dest := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := extractZip(archivePath, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "go", "bin", "go"))
	if err != nil {
		t.Fatalf("file not extracted: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q", got)
	}
}

// ── atomicWriteFile ─────────────────────────────────────────────────

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "version")

	if err := atomicWriteFile(path, []byte("1.25.5\n")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1.25.5\n" {
		t.Fatalf("got %q, want %q", got, "1.25.5\n")
	}

	// Overwrite atomically.
	if err := atomicWriteFile(path, []byte("1.26.0\n")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "1.26.0\n" {
		t.Fatalf("got %q after overwrite", got)
	}
}

// ── ResolveVersion ──────────────────────────────────────────────────

func TestResolveVersion_ExactPassthrough(t *testing.T) {
	m := &Manager{root: t.TempDir()}
	v, err := m.ResolveVersion(context.Background(), "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1.25.5" {
		t.Fatalf("got %q, want 1.25.5", v)
	}
}

func TestResolveVersion_InvalidInput(t *testing.T) {
	m := &Manager{root: t.TempDir()}
	_, err := m.ResolveVersion(context.Background(), "not-a-version")
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestResolveVersion_Latest(t *testing.T) {
	m := &Manager{
		root: t.TempDir(),
		cachedManifest: []releaseManifest{
			{Version: "go1.26rc1", Files: nil},
			{Version: "go1.25.5", Files: nil},
			{Version: "go1.25.4", Files: nil},
			{Version: "go1.24.3", Files: nil},
		},
	}
	v, err := m.ResolveVersion(context.Background(), "latest")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1.25.5" {
		t.Fatalf("got %q, want 1.25.5 (should skip rc)", v)
	}
}

func TestResolveVersion_Minor(t *testing.T) {
	m := &Manager{
		root: t.TempDir(),
		cachedManifest: []releaseManifest{
			{Version: "go1.25.5", Files: nil},
			{Version: "go1.25.4", Files: nil},
			{Version: "go1.24.3", Files: nil},
		},
	}
	v, err := m.ResolveVersion(context.Background(), "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1.25.5" {
		t.Fatalf("got %q, want 1.25.5", v)
	}
}

func TestResolveVersion_MinorNotFound(t *testing.T) {
	m := &Manager{
		root: t.TempDir(),
		cachedManifest: []releaseManifest{
			{Version: "go1.25.5", Files: nil},
		},
	}
	_, err := m.ResolveVersion(context.Background(), "1.99")
	if err == nil {
		t.Fatal("expected error for non-existent minor version")
	}
}

func TestResolveInstalledVersion_Latest(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	for _, version := range []string{"1.24.3", "1.25.4", "1.25.5"} {
		if err := os.MkdirAll(m.versionDir(version), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	v, err := m.ResolveInstalledVersion("latest")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1.25.5" {
		t.Fatalf("got %q, want 1.25.5", v)
	}
}

func TestResolveInstalledVersion_Minor(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	for _, version := range []string{"1.24.3", "1.25.4", "1.25.5"} {
		if err := os.MkdirAll(m.versionDir(version), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	v, err := m.ResolveInstalledVersion("1.25")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1.25.5" {
		t.Fatalf("got %q, want 1.25.5", v)
	}
}

func TestResolveInstalledVersion_MinorNotFound(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := os.MkdirAll(m.versionDir("1.25.5"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := m.ResolveInstalledVersion("1.24")
	if err == nil {
		t.Fatal("expected error for non-installed minor version")
	}
}

func TestResolveInstalledVersion_ExactNotInstalled(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}

	_, err := m.ResolveInstalledVersion("1.25.5")
	if err == nil {
		t.Fatal("expected error for non-installed exact version")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── compareVersions ─────────────────────────────────────────────────

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int // negative, zero, or positive
	}{
		{"1.25.5", "1.25.5", 0},
		{"1.25.5", "1.25.4", 1},
		{"1.25.4", "1.25.5", -1},
		{"1.26.0", "1.25.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.25", "1.25.0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := compareVersions(tt.a, tt.b)
			switch {
			case tt.want == 0 && got != 0:
				t.Errorf("compareVersions(%q, %q) = %d, want 0", tt.a, tt.b, got)
			case tt.want > 0 && got <= 0:
				t.Errorf("compareVersions(%q, %q) = %d, want positive", tt.a, tt.b, got)
			case tt.want < 0 && got >= 0:
				t.Errorf("compareVersions(%q, %q) = %d, want negative", tt.a, tt.b, got)
			}
		})
	}
}

// ── NewManager ──────────────────────────────────────────────────────

func TestNewManager_DefaultRoot(t *testing.T) {
	t.Setenv("FGM_DIR", "")
	m, err := NewManager("", nil)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".fgm")
	if m.Root() != want {
		t.Fatalf("root = %q, want %q", m.Root(), want)
	}
}

func TestNewManager_CustomRoot(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Root() != dir {
		t.Fatalf("root = %q, want %q", m.Root(), dir)
	}
}

func TestNewManager_EnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FGM_DIR", dir)
	m, err := NewManager("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Root() != dir {
		t.Fatalf("root = %q, want %q", m.Root(), dir)
	}
}

// ── ensureLayout ────────────────────────────────────────────────────

func TestEnsureLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fgm")
	m := &Manager{root: root, log: func(string, ...any) {}}

	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"versions", "downloads", "tmp", "bin"} {
		path := filepath.Join(root, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
		} else if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}
}

// ── cleanStaleTmp ───────────────────────────────────────────────────

func TestCleanStaleTmp(t *testing.T) {
	root := t.TempDir()
	m := &Manager{root: root, log: func(string, ...any) {}}

	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create stale entries.
	if err := os.MkdirAll(filepath.Join(tmpDir, "install-abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "install-abc123", "file.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "orphan.tmp"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	m.cleanStaleTmp()

	entries, _ := os.ReadDir(tmpDir)
	if len(entries) != 0 {
		t.Fatalf("expected tmp to be empty, got %d entries", len(entries))
	}
}

// ── moveDir ─────────────────────────────────────────────────────────

func TestMoveDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")

	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "go"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte("1.25.5"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveDir(src, dst); err != nil {
		t.Fatal(err)
	}

	// src should be gone.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("expected src to be removed after move")
	}

	// dst should have the files.
	got, err := os.ReadFile(filepath.Join(dst, "bin", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("got %q, want %q", got, "binary")
	}
}

// ── Full Manager lifecycle ──────────────────────────────────────────

// newTestManager creates a Manager with a temp root and a fake Go archive
// pre-placed in the downloads directory so Install doesn't need the network.
func newTestManager(t *testing.T, version string) *Manager {
	t.Helper()
	root := t.TempDir()
	m := &Manager{root: root, log: func(string, ...any) {}}

	// Build a minimal tar.gz that looks like a Go installation.
	for _, dir := range []string{m.downloadsDir(), m.versionsDir(), m.tmpDir(), m.binDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	archiveName, ext := fakeArchiveName(version)
	archivePath := filepath.Join(m.downloadsDir(), archiveName)

	if ext == ".tar.gz" {
		createFakeTarGz(t, archivePath, version)
	} else {
		createFakeZip(t, archivePath, version)
	}

	// Compute real checksum so verification passes.
	sha := fileSHA256(t, archivePath)

	// Inject manifest so no network call is made.
	osName, arch, archExt, err := platformReleaseParts()
	if err != nil {
		t.Fatal(err)
	}
	m.cachedManifest = []releaseManifest{
		{
			Version: "go" + version,
			Files: []releaseFile{
				{
					Filename: archiveName,
					OS:       osName,
					Arch:     arch,
					Version:  "go" + version,
					SHA256:   sha,
					Kind:     "archive",
				},
			},
		},
	}
	_ = archExt // used via fakeArchiveName

	return m
}

func fakeArchiveName(version string) (name string, ext string) {
	osName, arch, ext, err := platformReleaseParts()
	if err != nil {
		panic(err)
	}
	return "go" + version + "." + osName + "-" + arch + ext, ext
}

func createFakeTarGz(t *testing.T, path, version string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// go/bin/ directory
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "go/bin/", Mode: 0o755}); err != nil {
		t.Fatal(err)
	}

	// go/bin/go fake binary
	goContent := []byte("#!/bin/sh\necho go" + version)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "go/bin/go", Size: int64(len(goContent)), Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(goContent); err != nil {
		t.Fatal(err)
	}

	// go/bin/gofmt fake binary
	fmtContent := []byte("#!/bin/sh\necho gofmt" + version)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "go/bin/gofmt", Size: int64(len(fmtContent)), Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(fmtContent); err != nil {
		t.Fatal(err)
	}

	closeAll(t, tw, gw, f)
}

func createFakeZip(t *testing.T, path, version string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	w, err := zw.Create("go/bin/go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("go" + version)); err != nil {
		t.Fatal(err)
	}

	w, err = zw.Create("go/bin/gofmt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("gofmt" + version)); err != nil {
		t.Fatal(err)
	}

	closeAll(t, zw, f)
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestInstall(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")

	if err := m.Install(ctx, "1.25.5"); err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Version directory should exist with go binary.
	goPath := filepath.Join(m.versionDir("1.25.5"), "bin", "go")
	if _, err := os.Stat(goPath); err != nil {
		t.Fatalf("expected go binary at %s: %v", goPath, err)
	}

	// Installing again should be a no-op.
	if err := m.Install(ctx, "1.25.5"); err != nil {
		t.Fatalf("second Install failed: %v", err)
	}
}

func TestUse(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")

	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatalf("Use failed: %v", err)
	}

	// current-version file should contain the version.
	data, err := os.ReadFile(m.currentVersionFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "1.25.5" {
		t.Fatalf("current-version = %q, want 1.25.5", got)
	}

	// Shims should exist.
	for _, name := range []string{"go", "gofmt"} {
		shimPath := filepath.Join(m.binDir(), name)
		info, err := os.Stat(shimPath)
		if err != nil {
			t.Fatalf("shim %s not found: %v", name, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("shim %s is not executable", name)
		}
	}
}

func TestCurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")

	// No active version initially.
	v, err := m.Current()
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("expected empty current, got %q", v)
	}

	// After Use, current should be set.
	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}
	v, err = m.Current()
	if err != nil {
		t.Fatal(err)
	}
	if v != "1.25.5" {
		t.Fatalf("current = %q, want 1.25.5", v)
	}
}

func TestCurrent_LegacySymlinkMissingInstalledVersion(t *testing.T) {
	m := newTestManager(t, "1.25.5")

	target := m.versionDir("1.24.0")
	if err := os.Symlink(target, m.currentLink()); err != nil {
		t.Skipf("symlink unsupported in test environment: %v", err)
	}

	_, err := m.Current()
	if err == nil {
		t.Fatal("expected error for legacy current symlink pointing to missing version")
	}
	if !strings.Contains(err.Error(), "current version 1.24.0 is not installed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")

	// Empty before install.
	versions, current, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected empty list, got %v", versions)
	}
	if current != "" {
		t.Fatalf("expected empty current, got %q", current)
	}

	// After install+use.
	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}
	versions, current, err = m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != "1.25.5" {
		t.Fatalf("versions = %v, want [1.25.5]", versions)
	}
	if current != "1.25.5" {
		t.Fatalf("current = %q, want 1.25.5", current)
	}
}

func TestUninstall(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")
	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}

	if err := m.Uninstall("1.25.5"); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	// Version directory should be gone.
	if _, err := os.Stat(m.versionDir("1.25.5")); !os.IsNotExist(err) {
		t.Fatal("expected version directory to be removed")
	}

	// Current should be cleared.
	v, err := m.Current()
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("current = %q after uninstall, want empty", v)
	}
}

func TestUninstall_NotInstalled(t *testing.T) {
	m := newTestManager(t, "1.25.5")
	err := m.Uninstall("1.99.0")
	if err == nil {
		t.Fatal("expected error uninstalling non-existent version")
	}
}

func TestUninstall_NonCurrentVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")
	if err := m.Install(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}

	// Install a second version by creating its directory manually.
	if err := os.MkdirAll(filepath.Join(m.versionDir("1.24.0"), "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Use 1.25.5, then uninstall 1.24.0 — current should remain.
	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}

	if err := m.Uninstall("1.24.0"); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	v, _ := m.Current()
	if v != "1.25.5" {
		t.Fatalf("current = %q, want 1.25.5 after uninstalling different version", v)
	}
}

// ── Shim content ────────────────────────────────────────────────────

func TestShimContent(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")
	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}

	shimPath := filepath.Join(m.binDir(), "go")
	content, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatal(err)
	}

	shimStr := string(content)
	// Shim should reference FGM_DIR and current-version.
	if !strings.Contains(shimStr, "current-version") {
		t.Error("shim does not reference current-version file")
	}
	if !strings.Contains(shimStr, "fgm") {
		t.Error("shim does not reference fgm")
	}
}

// ── Prune ───────────────────────────────────────────────────────────

func TestPrune(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")

	// Install so there's a cached archive.
	if err := m.Install(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}

	// Downloads dir should have the archive.
	entries, _ := os.ReadDir(m.downloadsDir())
	if len(entries) == 0 {
		t.Fatal("expected cached archive after install")
	}

	removed, bytes, err := m.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("expected prune to remove files")
	}
	if bytes == 0 {
		t.Fatal("expected prune to report freed bytes")
	}

	// Downloads dir should be empty.
	entries, _ = os.ReadDir(m.downloadsDir())
	if len(entries) != 0 {
		t.Fatalf("expected downloads dir to be empty after prune, got %d entries", len(entries))
	}

	// Installed version should still exist.
	if !m.isInstalled("1.25.5") {
		t.Fatal("prune should not remove installed versions")
	}
}

func TestPrune_NothingToRemove(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	removed, bytes, err := m.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || bytes != 0 {
		t.Fatalf("expected nothing to prune, got removed=%d bytes=%d", removed, bytes)
	}
}

// ── List without existing directory ─────────────────────────────────

func TestList_NoVersionsDir(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	// Don't create any directories — List should return empty, not error.
	versions, current, err := m.List()
	if err != nil {
		t.Fatalf("List() on fresh root should not error: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected empty list, got %v", versions)
	}
	if current != "" {
		t.Fatalf("expected empty current, got %q", current)
	}
}
