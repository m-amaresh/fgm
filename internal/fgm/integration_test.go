package fgm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests that download real Go binaries from go.dev.
// Skip with: go test -short ./internal/fgm/

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test (use -short=false to run)")
	}
}

// testVersion is an old, small Go release used to keep downloads fast.
const testVersion = "1.21.0"
const testVersionAlt = "1.21.1"

func newRealManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	var logs []string
	m, err := NewManager(root, func(format string, _ ...any) {
		// Collect logs for verbose verification.
		logs = append(logs, format)
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// ── Manifest fetch ──────────────────────────────────────────────────

func TestIntegration_ManifestFetch(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)
	releases, err := m.manifest(ctx)
	if err != nil {
		t.Fatalf("manifest fetch failed: %v", err)
	}
	if len(releases) == 0 {
		t.Fatal("manifest returned zero releases")
	}

	// Should contain at least one stable release.
	found := false
	for _, r := range releases {
		v := strings.TrimPrefix(r.Version, "go")
		if isStableVersion(v) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no stable release found in manifest")
	}
}

// ── Manifest caching ────────────────────────────────────────────────

func TestIntegration_ManifestCache(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)

	// First call fetches from network.
	r1, err := m.manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Cache file should exist on disk.
	cachePath := m.manifestCachePath()
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("manifest cache not written to disk: %v", err)
	}

	// Second call should use in-memory cache (same pointer).
	r2, err := m.manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != len(r2) {
		t.Fatal("cached manifest differs from original")
	}

	// New manager should load from disk cache.
	m2, _ := NewManager(m.Root(), nil)
	r3, err := m2.manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r3) != len(r1) {
		t.Fatalf("disk cache returned %d releases, expected %d", len(r3), len(r1))
	}
}

// ── Resolve version ─────────────────────────────────────────────────

func TestIntegration_ResolveLatest(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)
	v, err := m.ResolveVersion(ctx, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if !isStableVersion(v) {
		t.Fatalf("latest resolved to non-stable version: %s", v)
	}
	if strings.Count(v, ".") < 1 {
		t.Fatalf("latest resolved to unexpected format: %s", v)
	}
	t.Logf("latest = %s", v)
}

func TestIntegration_ResolveMinor(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)
	v, err := m.ResolveVersion(ctx, "1.21")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "1.21") {
		t.Fatalf("1.21 resolved to %s, expected 1.21.x", v)
	}
	t.Logf("1.21 resolved to %s", v)
}

// ── Full install lifecycle ──────────────────────────────────────────

func TestIntegration_InstallAndUse(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)

	// Install a real Go version.
	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatalf("Install(%s) failed: %v", testVersion, err)
	}

	// Version directory should exist with a real go binary.
	goBin := filepath.Join(m.versionDir(testVersion), "bin", "go")
	info, err := os.Stat(goBin)
	if err != nil {
		t.Fatalf("go binary not found at %s: %v", goBin, err)
	}
	if info.Size() < 1000 {
		t.Fatalf("go binary suspiciously small: %d bytes", info.Size())
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("go binary is not executable")
	}

	// gofmt should also exist.
	gofmtBin := filepath.Join(m.versionDir(testVersion), "bin", "gofmt")
	if _, err := os.Stat(gofmtBin); err != nil {
		t.Fatalf("gofmt binary not found: %v", err)
	}

	// Use it.
	if err := m.Use(ctx, testVersion); err != nil {
		t.Fatalf("Use(%s) failed: %v", testVersion, err)
	}

	// Current should report the version.
	current, err := m.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current != testVersion {
		t.Fatalf("Current() = %q, want %q", current, testVersion)
	}

	// List should include it.
	versions, cur, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != testVersion {
		t.Fatalf("List() = %v, want [%s]", versions, testVersion)
	}
	if cur != testVersion {
		t.Fatalf("List() current = %q, want %q", cur, testVersion)
	}

	// Shims should exist and be executable.
	for _, name := range []string{"go", "gofmt"} {
		shimPath := filepath.Join(m.binDir(), name)
		si, err := os.Stat(shimPath)
		if err != nil {
			t.Fatalf("shim %s not found: %v", name, err)
		}
		if si.Mode()&0o111 == 0 {
			t.Fatalf("shim %s is not executable", name)
		}
		// Shim should contain the root path.
		content, _ := os.ReadFile(shimPath)
		if !strings.Contains(string(content), "current-version") {
			t.Fatalf("shim %s does not reference current-version", name)
		}
	}
}

// ── Install already installed is a no-op ────────────────────────────

func TestIntegration_InstallAlreadyInstalled(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)

	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	// Get mod time of go binary.
	goBin := filepath.Join(m.versionDir(testVersion), "bin", "go")
	info1, _ := os.Stat(goBin)

	// Install again — should be a no-op, not re-extract.
	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	info2, _ := os.Stat(goBin)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("second install modified the binary — should be a no-op")
	}
}

// ── Download cache reuse ────────────────────────────────────────────

func TestIntegration_DownloadCacheReuse(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)

	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	// Archive should exist in downloads dir.
	entries, _ := os.ReadDir(m.downloadsDir())
	if len(entries) == 0 {
		t.Fatal("no archive found in downloads dir after install")
	}

	archivePath := filepath.Join(m.downloadsDir(), entries[0].Name())
	info1, _ := os.Stat(archivePath)

	// Uninstall and reinstall — should reuse cached archive.
	if err := m.Uninstall(testVersion); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	info2, _ := os.Stat(archivePath)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("reinstall re-downloaded the archive instead of using cache")
	}
}

// ── Version switching ───────────────────────────────────────────────

func TestIntegration_SwitchVersions(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)

	// Install and use first version.
	if err := m.Use(ctx, testVersion); err != nil {
		t.Fatalf("Use(%s) failed: %v", testVersion, err)
	}

	cur, _ := m.Current()
	if cur != testVersion {
		t.Fatalf("current = %q, want %q", cur, testVersion)
	}

	// Install and switch to second version.
	if err := m.Use(ctx, testVersionAlt); err != nil {
		t.Fatalf("Use(%s) failed: %v", testVersionAlt, err)
	}

	cur, _ = m.Current()
	if cur != testVersionAlt {
		t.Fatalf("current = %q after switch, want %q", cur, testVersionAlt)
	}

	// Both should be listed.
	versions, _, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 installed versions, got %v", versions)
	}

	// Switch back.
	if err := m.Use(ctx, testVersion); err != nil {
		t.Fatal(err)
	}
	cur, _ = m.Current()
	if cur != testVersion {
		t.Fatalf("current = %q after switch back, want %q", cur, testVersion)
	}
}

// ── Uninstall ───────────────────────────────────────────────────────

func TestIntegration_UninstallCurrent(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)
	if err := m.Use(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	if err := m.Uninstall(testVersion); err != nil {
		t.Fatal(err)
	}

	// Version dir should be gone.
	if _, err := os.Stat(m.versionDir(testVersion)); !os.IsNotExist(err) {
		t.Fatal("version directory still exists after uninstall")
	}

	// Current should be empty.
	cur, err := m.Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur != "" {
		t.Fatalf("current = %q after uninstalling active version, want empty", cur)
	}
}

func TestIntegration_UninstallNonCurrent(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)
	if err := m.Use(ctx, testVersion); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(ctx, testVersionAlt); err != nil {
		t.Fatal(err)
	}

	// Uninstall the non-current version.
	if err := m.Uninstall(testVersionAlt); err != nil {
		t.Fatal(err)
	}

	// Current should still be the first version.
	cur, _ := m.Current()
	if cur != testVersion {
		t.Fatalf("current = %q, want %q after uninstalling other version", cur, testVersion)
	}

	versions, _, _ := m.List()
	if len(versions) != 1 || versions[0] != testVersion {
		t.Fatalf("List() = %v, want [%s]", versions, testVersion)
	}
}

func TestIntegration_UninstallNotInstalled(t *testing.T) {
	skipIfShort(t)

	m := newRealManager(t)
	err := m.Uninstall("1.99.0")
	if err == nil {
		t.Fatal("expected error uninstalling non-existent version")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ── Tmp cleanup ─────────────────────────────────────────────────────

func TestIntegration_TmpCleanedAfterInstall(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)
	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	// Tmp dir should be empty after successful install.
	entries, _ := os.ReadDir(m.tmpDir())
	if len(entries) != 0 {
		t.Fatalf("tmp dir has %d leftover entries after install", len(entries))
	}
}

// ── Verbose mode ────────────────────────────────────────────────────

func TestIntegration_VerboseOutput(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	root := t.TempDir()
	var logs []string
	m, _ := NewManager(root, func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		logs = append(logs, msg)
	})
	m.Verbose = true

	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	// Verbose mode should have produced diagnostic output.
	verboseCount := 0
	for _, log := range logs {
		if strings.Contains(log, "[verbose]") {
			verboseCount++
		}
	}
	if verboseCount == 0 {
		t.Fatal("verbose mode produced no [verbose] output")
	}

	// Should include key diagnostic info.
	allLogs := strings.Join(logs, "\n")
	for _, want := range []string{"fgm root:", "platform:", "archive:", "url:", "sha256:", "installing to:"} {
		if !strings.Contains(allLogs, want) {
			t.Errorf("verbose output missing %q", want)
		}
	}
	t.Logf("verbose produced %d diagnostic lines", verboseCount)
}

// ── Checksum verification is real ───────────────────────────────────

func TestIntegration_ChecksumActuallyVerified(t *testing.T) {
	skipIfShort(t)

	ctx := context.Background()
	m := newRealManager(t)

	// Install normally to get the archive.
	if err := m.Install(ctx, testVersion); err != nil {
		t.Fatal(err)
	}

	// Corrupt the cached archive.
	entries, _ := os.ReadDir(m.downloadsDir())
	if len(entries) == 0 {
		t.Fatal("no archive in downloads dir")
	}
	archivePath := filepath.Join(m.downloadsDir(), entries[0].Name())
	f, err := os.OpenFile(archivePath, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("CORRUPTED"), 0); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Remove installed version so it tries to re-extract.
	if err := os.RemoveAll(m.versionDir(testVersion)); err != nil {
		t.Fatal(err)
	}

	// Install should fail due to checksum mismatch.
	err = m.Install(ctx, testVersion)
	if err == nil {
		t.Fatal("expected checksum error with corrupted archive")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got: %v", err)
	}
}
