package fgm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── FindLocalVersion ────────────────────────────────────────────────

func TestFindLocalVersion_InWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeLocalVersion(t, dir, "1.24.0\n")

	version, path, err := FindLocalVersion(dir)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.24.0" {
		t.Errorf("version = %q, want 1.24.0", version)
	}
	if path != filepath.Join(dir, LocalVersionFile) {
		t.Errorf("path = %q, want the file in %s", path, dir)
	}
}

func TestFindLocalVersion_WalksUp(t *testing.T) {
	root := t.TempDir()
	writeLocalVersion(t, root, "  1.24.0  \n")
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	version, path, err := FindLocalVersion(nested)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.24.0" {
		t.Errorf("version = %q, want 1.24.0 (whitespace trimmed)", version)
	}
	if path != filepath.Join(root, LocalVersionFile) {
		t.Errorf("path = %q, want the file in %s", path, root)
	}
}

func TestFindLocalVersion_NearestWins(t *testing.T) {
	root := t.TempDir()
	writeLocalVersion(t, root, "1.24.0\n")
	nested := filepath.Join(root, "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalVersion(t, nested, "1.25.5\n")

	version, _, err := FindLocalVersion(nested)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.25.5" {
		t.Errorf("version = %q, want the nearest pin 1.25.5", version)
	}
}

func TestFindLocalVersion_None(t *testing.T) {
	version, path, err := FindLocalVersion(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if version != "" || path != "" {
		t.Errorf("expected no pin, got version=%q path=%q", version, path)
	}
}

// An empty marker stops the walk and yields no version, so the caller falls
// back to the global one rather than picking up a parent's pin. This mirrors
// what the shims do.
func TestFindLocalVersion_EmptyFileStopsWalk(t *testing.T) {
	root := t.TempDir()
	writeLocalVersion(t, root, "1.24.0\n")
	nested := filepath.Join(root, "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalVersion(t, nested, "   \n")

	version, path, err := FindLocalVersion(nested)
	if err != nil {
		t.Fatal(err)
	}
	if version != "" {
		t.Errorf("version = %q, want empty", version)
	}
	if path != filepath.Join(nested, LocalVersionFile) {
		t.Errorf("path = %q, want the empty file that stopped the walk", path)
	}
}

// The shims paste this value into the path of the binary they exec, so a
// .go-version that is not a plain version number has to be refused rather
// than resolved — both here and in the generated shims.
func TestFindLocalVersion_RejectsAnythingButAVersion(t *testing.T) {
	for name, content := range map[string]string{
		"parent traversal":   "../../elsewhere",
		"windows traversal":  `..\..\elsewhere`,
		"absolute path":      "/usr/local/go",
		"command substitute": "1.2$(id)",
		"shell metachars":    "1.2\"; id; \"",
		"repeated dots":      "1..2",
		"trailing dot":       "1.24.",
		"leading dot":        ".24.0",
		"letters":            "tip",
		"interior space":     "1.2 5",
		// Nothing that reads a marker resolves versions, so a minor version
		// names no toolchain: installing it would leave the pin just as
		// broken.
		"minor version":  "1.25",
		"four component": "1.2.3.4",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeLocalVersion(t, dir, content+"\n")

			version, _, err := FindLocalVersion(dir)
			if err == nil {
				t.Fatalf("accepted %q as version %q", content, version)
			}
			if !strings.Contains(err.Error(), LocalVersionFile) {
				t.Errorf("error should name the file, got: %v", err)
			}
		})
	}
}

// Only the first line counts, so trailing junk cannot smuggle anything past
// the check above. The shims read these files the same way.
func TestFindLocalVersion_UsesFirstLineOnly(t *testing.T) {
	dir := t.TempDir()
	writeLocalVersion(t, dir, "1.24.0\n../../elsewhere\n")

	version, _, err := FindLocalVersion(dir)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.24.0" {
		t.Errorf("version = %q, want 1.24.0", version)
	}
}

// isInstalled joins the version into a path, so an escaping value must not be
// able to report a directory outside the versions dir as an installed
// toolchain.
func TestIsInstalled_RejectsPathEscape(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	// A real directory one level outside the versions dir.
	outside := filepath.Join(m.root, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"../elsewhere", `..\elsewhere`, "..", "/usr"} {
		if m.isInstalled(version) {
			t.Errorf("isInstalled(%q) = true, want false", version)
		}
	}
}

// ── Effective ───────────────────────────────────────────────────────

func TestEffective_LocalBeatsGlobal(t *testing.T) {
	m := newShimManager(t, "1.25.5", "1.24.0")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLocalVersion(t, dir, "1.24.0\n")

	got, err := m.Effective(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.24.0" {
		t.Errorf("version = %q, want 1.24.0", got.Version)
	}
	if !got.IsLocal() {
		t.Error("expected the source to be reported as local")
	}
}

func TestEffective_FallsBackToGlobal(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	got, err := m.Effective(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.25.5" {
		t.Errorf("version = %q, want 1.25.5", got.Version)
	}
	if got.IsLocal() {
		t.Error("expected the source to be the global marker")
	}
}

func TestEffective_PinnedVersionNotInstalled(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLocalVersion(t, dir, "1.19.0\n")

	_, err := m.Effective(dir)
	if err == nil {
		t.Fatal("expected an error for a pin that is not installed")
	}
	if !strings.Contains(err.Error(), "1.19.0") || !strings.Contains(err.Error(), LocalVersionFile) {
		t.Errorf("error should name the version and the file, got: %v", err)
	}
}

func TestEffective_RejectsHostilePin(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLocalVersion(t, dir, "../../elsewhere\n")

	// Reporting the global version here would have fgm disagree with the
	// shims, which refuse to run at all.
	if got, err := m.Effective(dir); err == nil {
		t.Fatalf("expected an error, got %+v", got)
	}
}

func TestEffective_NothingActive(t *testing.T) {
	m := newShimManager(t, "1.25.5")

	got, err := m.Effective(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "" {
		t.Errorf("version = %q, want empty", got.Version)
	}
}

// ── UseLocal ────────────────────────────────────────────────────────

func TestUseLocal_WritesPinAndInstallsShims(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, "1.25.5")
	dir := t.TempDir()

	path, err := m.UseLocal(ctx, "1.25.5", dir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "1.25.5" {
		t.Errorf("%s contains %q, want 1.25.5", LocalVersionFile, string(data))
	}

	// The pin must work without a prior global activation, so the shims have
	// to exist even though the global marker was never written.
	if _, err := os.Stat(filepath.Join(m.binDir(), shimFilename("go"))); err != nil {
		t.Errorf("expected shims to be installed: %v", err)
	}
	if _, err := os.Stat(m.currentVersionFile()); !os.IsNotExist(err) {
		t.Error("UseLocal must not write the global current-version marker")
	}
}

// ── notInstalledError ───────────────────────────────────────────────

func TestNotInstalledError_SuggestsSameMinor(t *testing.T) {
	m := newShimManager(t, "1.25.5", "1.25.1")

	err := m.notInstalledError("1.25.9")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"1.25.9", "1.25.1", "1.25.5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s, got: %v", want, err)
		}
	}
}

func TestNotInstalledError_NoSuggestionWhenNoneMatch(t *testing.T) {
	m := newShimManager(t, "1.25.5")

	err := m.notInstalledError("1.19.0")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "installed:") {
		t.Errorf("expected no suggestion for an unrelated minor, got: %v", err)
	}
}

// ── Prune ───────────────────────────────────────────────────────────

func TestPrune_KeepsInFlightDownloads(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}

	cached := filepath.Join(m.downloadsDir(), "go1.25.5.linux-amd64.tar.gz")
	if err := os.WriteFile(cached, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	inFlight := filepath.Join(m.downloadsDir(), partialPrefix+"123")
	if err := os.WriteFile(inFlight, []byte("downloading"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.Prune(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Error("expected the cached archive to be removed")
	}
	if _, err := os.Stat(inFlight); err != nil {
		t.Errorf("prune must not delete an in-flight download: %v", err)
	}
}

func TestPrune_ClearsAbandonedTmp(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}

	leftover := filepath.Join(m.tmpDir(), "install-abc")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "partial"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeStale(t, leftover)

	removed, _, err := m.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("expected prune to report the scratch directory")
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Error("expected scratch space to be cleared")
	}
}

// Pruning while an install is extracting must not pull the scratch directory
// out from under it.
func TestPrune_KeepsRecentTmp(t *testing.T) {
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}

	inUse := filepath.Join(m.tmpDir(), "install-inflight")
	if err := os.MkdirAll(inUse, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inUse); err != nil {
		t.Errorf("prune must not delete scratch space an install is using: %v", err)
	}
}

// ── copyDir (the cross-device fallback in moveDir) ───────────────────

func TestCopyDir_PreservesTreeAndExecutableBits(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dest")

	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "go"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte("go1.25.5"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "go1.25.5" {
		t.Errorf("VERSION = %q", string(data))
	}

	info, err := os.Stat(filepath.Join(dst, "bin", "go"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not expose Unix executable permission bits; the copied
	// file remains executable there by virtue of its .exe image.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Error("expected the executable bit to survive the copy")
	}
}
