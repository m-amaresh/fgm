package fgm

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
		{"absolute unix path blocked", "/tmp/extract", "/etc/passwd", true},
		{"absolute windows drive path blocked", "/tmp/extract", `C:\Windows\System32`, true},
		{"absolute windows slash path blocked", "/tmp/extract", "C:/Windows/System32", true},
		{"absolute windows unc path blocked", "/tmp/extract", `\\server\share\go.exe`, true},
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

// ── validateSHA256 ─────────────────────────────────────────────────

func TestValidateSHA256(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid lowercase", strings.Repeat("a", 64), false},
		{"valid uppercase", strings.Repeat("A", 64), false},
		{"too short", strings.Repeat("a", 63), true},
		{"too long", strings.Repeat("a", 65), true},
		{"non hex", strings.Repeat("g", 64), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSHA256(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSHA256(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

// ── cancellation ───────────────────────────────────────────────────

func TestDownloadFile_CanceledBeforeRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := filepath.Join(t.TempDir(), "archive.tar.gz")
	err := downloadFile(ctx, "http://127.0.0.1:1/archive.tar.gz", dest)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("downloadFile error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("download destination exists after canceled request: %v", statErr)
	}
}

func TestDownloadFile_CanceledDuringBodyCopy(t *testing.T) {
	bodyStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("partial archive")); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(bodyStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dest := filepath.Join(t.TempDir(), "archive.tar.gz")
	errCh := make(chan error, 1)
	go func() {
		errCh <- downloadFile(ctx, server.URL, dest)
	}()

	select {
	case <-bodyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("download body did not start")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("downloadFile error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("downloadFile did not return after context cancellation")
	}
}

func TestVerifyChecksum_CanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := verifyChecksum(ctx, path, strings.Repeat("a", 64))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verifyChecksum error = %v, want context.Canceled", err)
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
	// inside the destination and victimDir/pwned is never created.
	archivePath := filepath.Join(t.TempDir(), "evil.tar.gz")
	victimDir := t.TempDir()
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Entry 1: go/evil -> victimDir (symlink — will be skipped)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     "go/evil",
		Linkname: victimDir,
	}); err != nil {
		t.Fatal(err)
	}

	// Entry 2: go/evil/pwned (would write to victimDir/pwned if symlink existed)
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

	if err := extractTarGz(context.Background(), archivePath, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The symlink was skipped, so the file never escaped to victimDir.
	if _, err := os.Stat(filepath.Join(victimDir, "pwned")); err == nil {
		t.Fatal("malicious file was written through symlink into victim dir")
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

	if err := extractTarGz(context.Background(), archivePath, dest); err != nil {
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

// ── extractZip: symlink entries are skipped ────────────────────────

func TestExtractZip_SkipsSymlinks(t *testing.T) {
	// Build a zip whose first entry is a symlink, then a regular file that
	// would escape through it. Skipping the symlink keeps both entries
	// contained inside dest.
	archivePath := filepath.Join(t.TempDir(), "evil.zip")
	victimDir := t.TempDir()
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	// Entry 1: go/evil — symlink (will be skipped)
	header := &zip.FileHeader{Name: "go/evil"}
	header.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(victimDir)); err != nil {
		t.Fatal(err)
	}

	// Entry 2: go/evil/pwned — would write through the symlink if it existed.
	w, err = zw.Create("go/evil/pwned")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("pwned")); err != nil {
		t.Fatal(err)
	}

	closeAll(t, zw, f)

	dest := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := extractZip(context.Background(), archivePath, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(victimDir, "pwned")); err == nil {
		t.Fatal("malicious file was written through symlink into victim dir")
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

	if err := extractZip(context.Background(), archivePath, dest); err != nil {
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

// ── malformed archive handling ──────────────────────────────────────

func TestExtractTarFile_RejectsNegativeSize(t *testing.T) {
	// A crafted tar header with a negative Size would otherwise pass the
	// `> maxExtractSize` check via signed comparison. Guard added explicitly.
	header := &tar.Header{Name: "go/bad", Size: -1, Typeflag: tar.TypeReg, Mode: 0o644}
	tr := tar.NewReader(&bytes.Buffer{})
	target := filepath.Join(t.TempDir(), "out")
	_, err := extractTarFile(context.Background(), tr, header, target, 0)
	if err == nil {
		t.Fatal("expected error for negative declared size")
	}
	if !strings.Contains(err.Error(), "negative declared size") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("target file was created despite negative-size rejection")
	}
}

func TestExtractTarGz_RejectsCorruptGzipStream(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	if err := os.WriteFile(archivePath, []byte("not a valid gzip stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := extractTarGz(context.Background(), archivePath, t.TempDir())
	if err == nil {
		t.Fatal("expected error for corrupt gzip")
	}
	if !strings.Contains(err.Error(), "gzip") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractZip_RejectsCorruptArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "corrupt.zip")
	if err := os.WriteFile(archivePath, []byte("not a valid zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := extractZip(context.Background(), archivePath, t.TempDir())
	if err == nil {
		t.Fatal("expected error for corrupt zip")
	}
	if !strings.Contains(err.Error(), "open zip archive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractZip_RejectsOversizedDeclaredEntry(t *testing.T) {
	// CreateRaw lets us hand-craft a header whose UncompressedSize64 lies
	// about the entry being larger than the safety cap. The cap check fires
	// before any I/O, so the actual stored bytes can be tiny.
	archivePath := filepath.Join(t.TempDir(), "oversized.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	raw := []byte("a")
	header := &zip.FileHeader{Name: "go/big", Method: zip.Store}
	header.SetMode(0o644)
	header.CompressedSize64 = uint64(len(raw))
	header.UncompressedSize64 = uint64(maxExtractSize) + 1
	header.CRC32 = crc32.ChecksumIEEE(raw)

	w, err := zw.CreateRaw(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	closeAll(t, zw, f)

	err = extractZip(context.Background(), archivePath, t.TempDir())
	if err == nil {
		t.Fatal("expected error for oversized declared entry")
	}
	if !strings.Contains(err.Error(), "exceeds maximum extraction size") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractZip_RejectsBadCRC(t *testing.T) {
	// Sanity: archive/zip itself rejects entries whose stored CRC does not
	// match the data, so a tampered archive surfaces as an extraction error
	// rather than a silently-corrupted install. The fgm extractor wraps the
	// underlying error in a `write file` message.
	archivePath := filepath.Join(t.TempDir(), "badcrc.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	raw := []byte("real contents")
	header := &zip.FileHeader{Name: "go/badcrc", Method: zip.Store}
	header.SetMode(0o644)
	header.CompressedSize64 = uint64(len(raw))
	header.UncompressedSize64 = uint64(len(raw))
	header.CRC32 = crc32.ChecksumIEEE([]byte("different bytes")) // wrong on purpose

	w, err := zw.CreateRaw(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	closeAll(t, zw, f)

	err = extractZip(context.Background(), archivePath, t.TempDir())
	if err == nil {
		t.Fatal("expected error for bad CRC")
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

// ── Available ──────────────────────────────────────────────────────

func TestAvailable_LatestPerMinor(t *testing.T) {
	osName, arch, ext, err := platformReleaseParts()
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		root: t.TempDir(),
		cachedManifest: []releaseManifest{
			testRelease("go1.26.0", osName, arch, ext),
			testRelease("go1.25.5", osName, arch, ext),
			testRelease("go1.25.4", osName, arch, ext),
			testRelease("go1.24.10", osName, arch, ext),
			testRelease("go1.27rc1", osName, arch, ext),
			testRelease("go1.23.12", "plan9", arch, ext),
		},
	}

	versions, err := m.Available(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.26.0", "1.25.5", "1.24.10"}
	if !slices.Equal(versions, want) {
		t.Fatalf("Available(false) = %v, want %v", versions, want)
	}
}

func TestAvailable_AllStableVersions(t *testing.T) {
	osName, arch, ext, err := platformReleaseParts()
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		root: t.TempDir(),
		cachedManifest: []releaseManifest{
			testRelease("go1.25.5", osName, arch, ext),
			testRelease("go1.25.4", osName, arch, ext),
			testRelease("go1.25rc1", osName, arch, ext),
		},
	}

	versions, err := m.Available(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.25.5", "1.25.4"}
	if !slices.Equal(versions, want) {
		t.Fatalf("Available(true) = %v, want %v", versions, want)
	}
}

func testRelease(version, osName, arch, ext string) releaseManifest {
	return releaseManifest{
		Version: version,
		Files: []releaseFile{{
			Filename: version + "." + osName + "-" + arch + ext,
			OS:       osName,
			Arch:     arch,
			Version:  version,
			SHA256:   strings.Repeat("a", 64),
			Kind:     "archive",
		}},
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

func TestList_StaleCurrentVersionWarnsAndContinues(t *testing.T) {
	var logs []string
	m := &Manager{root: t.TempDir(), log: func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}}

	if err := os.MkdirAll(m.versionDir("1.25.5"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.currentVersionFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(m.currentVersionFile(), []byte("1.99.0\n")); err != nil {
		t.Fatal(err)
	}

	versions, current, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != "1.25.5" {
		t.Fatalf("versions = %v, want [1.25.5]", versions)
	}
	if current != "" {
		t.Fatalf("current = %q, want empty for stale marker", current)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "current version 1.99.0 is not installed") {
		t.Fatalf("expected stale current warning, got logs %v", logs)
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
	if !strings.Contains(shimStr, "fgm: no active Go version; run: fgm use latest") {
		t.Error("shim does not include recovery hint for missing active version")
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

// ── Env ────────────────────────────────────────────────────────────

func TestEnv_NoCurrentVersion(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}

	info := m.Env()
	if info.FGMDir != m.root {
		t.Fatalf("FGMDir = %q, want %q", info.FGMDir, m.root)
	}
	if info.ShimDir != m.binDir() {
		t.Fatalf("ShimDir = %q, want %q", info.ShimDir, m.binDir())
	}
	if info.VersionsDir != m.versionsDir() {
		t.Fatalf("VersionsDir = %q, want %q", info.VersionsDir, m.versionsDir())
	}
	if info.DownloadsDir != m.downloadsDir() {
		t.Fatalf("DownloadsDir = %q, want %q", info.DownloadsDir, m.downloadsDir())
	}
	if info.CurrentVersion != "" {
		t.Fatalf("CurrentVersion = %q, want empty", info.CurrentVersion)
	}
	if info.CurrentError != "" {
		t.Fatalf("CurrentError = %q, want empty", info.CurrentError)
	}
	if info.Platform == "" {
		t.Fatal("Platform should not be empty")
	}
}

func TestEnv_StaleCurrentVersionReportsError(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := os.MkdirAll(m.versionDir("1.25.5"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(m.currentVersionFile(), []byte("1.99.0\n")); err != nil {
		t.Fatal(err)
	}

	info := m.Env()
	if info.CurrentError == "" {
		t.Fatal("expected current version error")
	}
	if !strings.Contains(info.CurrentError, "current version 1.99.0 is not installed") {
		t.Fatalf("unexpected CurrentError: %q", info.CurrentError)
	}
}

// ── Doctor ─────────────────────────────────────────────────────────

func TestDoctor_HealthyInstall(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")
	if err := m.Use(ctx, "1.25.5"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", m.binDir()+string(os.PathListSeparator)+os.Getenv("PATH"))

	checks := m.Doctor()
	if len(checks) == 0 {
		t.Fatal("expected doctor checks")
	}
	for _, check := range checks {
		if check.Status == DoctorFail {
			t.Fatalf("unexpected failing check: %+v", check)
		}
	}
	if !hasDoctorCheck(checks, "active version", DoctorOK) {
		t.Fatalf("expected active version check to pass, got %+v", checks)
	}
	if !hasDoctorCheck(checks, "PATH", DoctorOK) {
		t.Fatalf("expected PATH check to pass, got %+v", checks)
	}
	if !hasDoctorCheck(checks, "go shim", DoctorOK) {
		t.Fatalf("expected go shim check to pass, got %+v", checks)
	}
}

func TestDoctor_StaleCurrentVersionFails(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := os.MkdirAll(m.versionDir("1.25.5"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(m.currentVersionFile(), []byte("1.99.0\n")); err != nil {
		t.Fatal(err)
	}

	checks := m.Doctor()
	if !hasDoctorCheck(checks, "active version", DoctorFail) {
		t.Fatalf("expected stale active version check to fail, got %+v", checks)
	}
}

func hasDoctorCheck(checks []DoctorCheck, name, status string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
