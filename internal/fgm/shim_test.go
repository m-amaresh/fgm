package fgm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGoEnv switches the test binary into standing in for a go binary. See
// fakeGoMain and installFakeGo.
const fakeGoEnv = "FGM_TEST_FAKE_GO"

// fakeGoMain replaces the real go binary when the test binary has been copied
// into a version directory. It reports the version directory it was run from,
// which is how the shim tests tell which toolchain the shim resolved, and
// exits 7 on demand so exit-code propagation can be checked.
func fakeGoMain() {
	exe, err := os.Executable()
	if err != nil {
		os.Stderr.WriteString("fake go: " + err.Error() + "\n")
		os.Exit(2)
	}
	version := filepath.Base(filepath.Dir(filepath.Dir(exe)))
	args := os.Args[1:]
	os.Stdout.WriteString("go " + version + " args:" + strings.Join(args, " ") + "\n")
	if len(args) > 0 && args[0] == "exit7" {
		os.Exit(7)
	}
}

// newShimManager returns a Manager whose layout contains a fake toolchain for
// each named version, with the shims installed.
func newShimManager(t *testing.T, versions ...string) *Manager {
	t.Helper()
	m := &Manager{root: t.TempDir(), log: func(string, ...any) {}}
	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		installFakeGo(t, m, v)
	}
	if err := m.installShims(); err != nil {
		t.Fatal(err)
	}
	return m
}

// installFakeGo puts a stand-in for the go binary in a version's bin dir.
func installFakeGo(t *testing.T, m *Manager, version string) {
	t.Helper()
	binDir := filepath.Join(m.versionDir(version), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		script := "#!/bin/sh\necho \"go " + version + " args:$*\"\n[ \"${1:-}\" = exit7 ] && exit 7\nexit 0\n"
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}

	// cmd.exe will not exec a script named go.exe, so the stand-in has to be
	// a real executable image: the test binary itself, which TestMain routes
	// to fakeGoMain when FGM_TEST_FAKE_GO is set.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(self, filepath.Join(binDir, "go.exe"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runShim executes the go shim from workDir with FGM_DIR deliberately unset,
// so the root baked into the script is what gets exercised.
func runShim(t *testing.T, m *Manager, workDir string, args ...string) (string, error) {
	t.Helper()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// A .cmd file is a script, not an executable image: only a command
		// interpreter can run it.
		cmd = exec.Command(comSpec(), append([]string{"/d", "/c", m.shimPath("go")}, args...)...)
	} else {
		cmd = exec.Command(m.shimPath("go"), args...)
	}
	cmd.Dir = workDir
	cmd.Env = shimEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// shimEnv is the environment the shim runs under: enough for a shell to
// function, the switch that turns the copied test binary into a fake go, and
// no FGM_DIR.
func shimEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), fakeGoEnv + "=1"}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "SystemDrive", "ComSpec", "PATHEXT", "TEMP", "TMP"} {
			if v := os.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	}
	return env
}

func comSpec() string {
	if v := os.Getenv("ComSpec"); v != "" {
		return v
	}
	return "cmd.exe"
}

func writeLocalVersion(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, LocalVersionFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShim_UsesGlobalMarker(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	out, err := runShim(t, m, t.TempDir(), "build", "./...")
	if err != nil {
		t.Fatalf("shim failed: %v (%s)", err, out)
	}
	if out != "go 1.25.5 args:build ./..." {
		t.Errorf("unexpected shim output: %q", out)
	}
}

func TestShim_LegacyReleaseMarkers(t *testing.T) {
	for _, version := range []string{"1.0", "1.9", "1.19", "1.20"} {
		t.Run(version, func(t *testing.T) {
			m := newShimManager(t, version)
			if err := m.Activate(version); err != nil {
				t.Fatal(err)
			}
			work := t.TempDir()
			for _, local := range []bool{false, true} {
				if local {
					writeLocalVersion(t, work, version+"\n")
				}
				effective, err := m.Effective(work)
				if err != nil || effective.Version != version {
					t.Fatalf("effective = %+v, %v", effective, err)
				}
				out, err := runShim(t, m, work, "version")
				if err != nil || out != "go "+version+" args:version" {
					t.Fatalf("shim = %q, %v", out, err)
				}
			}
			if err := m.Uninstall(version); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(m.currentVersionFile()); !os.IsNotExist(err) {
				t.Fatalf("global marker not cleared: %v", err)
			}
		})
	}
}

func TestShim_UnreadableMarkerFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions")
	}
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "local"}[local], func(t *testing.T) {
			m := newShimManager(t, "1.25.5", "1.24.0")
			if err := m.Activate("1.25.5"); err != nil {
				t.Fatal(err)
			}
			work := t.TempDir()
			path := m.currentVersionFile()
			if local {
				writeLocalVersion(t, work, "1.24.0\n")
				path = filepath.Join(work, LocalVersionFile)
			}
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			if _, err := os.ReadFile(path); err == nil {
				t.Skip("account can read files without permission bits")
			}
			if _, err := m.Effective(work); err == nil {
				t.Fatal("expected resolver to reject unreadable marker")
			}
			out, err := runShim(t, m, work, "version")
			if err == nil || !strings.Contains(out, "cannot read version file") || strings.Contains(out, "args:version") {
				t.Fatalf("expected failure without executing a toolchain; output=%q error=%v", out, err)
			}
		})
	}
}

func TestShim_LocalVersionBeatsGlobal(t *testing.T) {
	m := newShimManager(t, "1.25.5", "1.24.0")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	writeLocalVersion(t, work, "1.24.0\n")

	out, err := runShim(t, m, work)
	if err != nil {
		t.Fatalf("shim failed: %v (%s)", err, out)
	}
	if !strings.HasPrefix(out, "go 1.24.0") {
		t.Errorf("expected the pinned version to win, got %q", out)
	}
}

func TestShim_WalksUpToParentDirectory(t *testing.T) {
	m := newShimManager(t, "1.25.5", "1.24.0")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	writeLocalVersion(t, root, "1.24.0\n")
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runShim(t, m, nested)
	if err != nil {
		t.Fatalf("shim failed: %v (%s)", err, out)
	}
	if !strings.HasPrefix(out, "go 1.24.0") {
		t.Errorf("expected the parent pin to apply, got %q", out)
	}
}

func TestShim_EmptyLocalVersionFallsBackToGlobal(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	writeLocalVersion(t, work, "\n")

	out, err := runShim(t, m, work)
	if err != nil {
		t.Fatalf("shim failed: %v (%s)", err, out)
	}
	if !strings.HasPrefix(out, "go 1.25.5") {
		t.Errorf("expected fallback to the global version, got %q", out)
	}
}

// The whole point of walking up is that a directory with no pin anywhere
// above it still resolves. The Windows shim used to spin here forever, since
// the walk turned "C:\" into "C:", whose parent is the working directory.
func TestShim_NoActiveVersion(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.installShims(); err != nil {
		t.Fatal(err)
	}

	out, err := runShim(t, m, t.TempDir())
	if err == nil {
		t.Fatalf("expected failure with no active version, got %q", out)
	}
	if !strings.Contains(out, "no active Go version") {
		t.Errorf("expected a recovery hint, got %q", out)
	}
}

func TestShim_PinnedVersionNotInstalled(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	writeLocalVersion(t, work, "1.19.0\n")

	out, err := runShim(t, m, work)
	if err == nil {
		t.Fatalf("expected failure for an uninstalled pin, got %q", out)
	}
	if !strings.Contains(out, "fgm install 1.19.0") {
		t.Errorf("expected an install hint naming the version, got %q", out)
	}
}

func TestShim_HandlesRootWithSpaces(t *testing.T) {
	base := filepath.Join(t.TempDir(), "go tools", "fgm home")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{root: base, log: func(string, ...any) {}}
	if err := m.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	installFakeGo(t, m, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	out, err := runShim(t, m, t.TempDir())
	if err != nil {
		t.Fatalf("shim failed for a root containing spaces: %v (%s)", err, out)
	}
	if !strings.HasPrefix(out, "go 1.25.5") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestShim_PassesThroughExitCode(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	_, err := runShim(t, m, t.TempDir(), "exit7")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an exit error, got %v", err)
	}
	if got := exitErr.ExitCode(); got != 7 {
		t.Errorf("exit code = %d, want 7 (the toolchain's own status)", got)
	}
}

// The version from a .go-version becomes a path component inside the shim,
// and the shim resolves nothing — so anything but an exact version has to be
// refused, whether it is hostile or merely inexact.
func TestShim_RejectsAnythingButAnExactVersion(t *testing.T) {
	m := newShimManager(t, "1.25.5")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	// A toolchain planted outside the versions directory, which the escaping
	// pins below aim at.
	outside := filepath.Join(m.root, "elsewhere")
	outsideManager := &Manager{root: outside, log: func(string, ...any) {}}
	installFakeGo(t, outsideManager, "escaped")

	pins := map[string]string{
		"parent traversal":    "../elsewhere/versions/escaped",
		"windows traversal":   `..\elsewhere\versions\escaped`,
		"absolute path":       "/usr",
		"command substitute":  "1.2$(id)",
		"shell metachars":     "1.2\"; id; \"",
		"batch metachars":     `1.2"& echo pwned &"`,
		"repeated dots":       "1..2",
		"trailing dot":        "1.24.",
		"not a version":       "tip",
		"minor version":       "1.25",
		"modern boundary":     "1.21",
		"noncanonical legacy": "1.020",
		"future major":        "2.0",
		"four component":      "1.2.3.4",
	}
	for name, pin := range pins {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			writeLocalVersion(t, work, pin+"\n")

			out, err := runShim(t, m, work, "version")
			if err == nil {
				t.Fatalf("shim accepted %q: %s", pin, out)
			}
			if !strings.Contains(out, "does not contain a Go version") {
				t.Errorf("expected a rejection naming the file, got %q", out)
			}
			if strings.Contains(out, "escaped") || strings.Contains(out, "pwned") {
				t.Errorf("shim resolved a version outside its root: %q", out)
			}
		})
	}
}

// fgm writes the pin itself, but people edit these files by hand, so the
// shims read them the same way fgm does: first line, surrounding whitespace
// ignored.
func TestShim_ReadsFirstLineOfLocalVersion(t *testing.T) {
	m := newShimManager(t, "1.25.5", "1.24.0")
	if err := m.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	contents := map[string]string{
		"padded":           "  1.24.0  \n",
		"no trailing line": "1.24.0",
		"crlf":             "1.24.0\r\n",
	}
	if runtime.GOOS != "windows" {
		// cmd.exe's `set /p` ends a line at CRLF or EOF, so a file with bare
		// LF separators reads whole and lands on the strict-validation path
		// instead. That is a refusal, never a wrong version, and fgm writes
		// single-line files itself.
		contents["trailing lines"] = "1.24.0\nanything after the first line is ignored\n"
	}

	for name, content := range contents {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			writeLocalVersion(t, work, content)

			out, err := runShim(t, m, work, "version")
			if err != nil {
				t.Fatalf("shim failed for %q: %v (%s)", content, err, out)
			}
			if !strings.HasPrefix(out, "go 1.24.0") {
				t.Errorf("expected go 1.24.0 for %q, got %q", content, out)
			}
		})
	}
}

func TestWindowsShim_BakesInRoot(t *testing.T) {
	m := &Manager{root: `D:\tools\fgm`, log: func(string, ...any) {}}
	content := m.windowsShim("go")

	if !strings.Contains(content, `set "FGM_DIR=D:\tools\fgm"`) {
		t.Errorf("windows shim does not bake in the root:\n%s", content)
	}
	if strings.Contains(content, "USERPROFILE") {
		t.Errorf("windows shim still assumes %%USERPROFILE%%:\n%s", content)
	}
	for _, want := range []string{LocalVersionFile, "current-version", `bin\go.exe`, "fgm install"} {
		if !strings.Contains(content, want) {
			t.Errorf("windows shim missing %q:\n%s", want, content)
		}
	}
}

func TestWindowsShim_EscapesPercentInRoot(t *testing.T) {
	m := &Manager{root: `C:\pct%dir\fgm`, log: func(string, ...any) {}}
	if !strings.Contains(m.windowsShim("go"), `set "FGM_DIR=C:\pct%%dir\fgm"`) {
		t.Errorf("percent sign in the root was not escaped:\n%s", m.windowsShim("go"))
	}
}

// The untrusted value must never be expanded by the batch parser: `for /f`
// re-parses what it assigns, so reading a version file with it would run a
// .go-version holding `1.2"& del x &"` as a command.
func TestWindowsShim_ReadsVersionFilesWithSetP(t *testing.T) {
	m := &Manager{root: `C:\fgm`, log: func(string, ...any) {}}
	content := m.windowsShim("go")

	for line := range strings.SplitSeq(content, "\r\n") {
		if strings.Contains(line, "for /f") && strings.Contains(line, LocalVersionFile) {
			t.Errorf("windows shim parses the version file with for /f: %q", line)
		}
	}
	if !strings.Contains(content, `set /p "FGM_VERSION="<"%FGM_SOURCE%"`) {
		t.Errorf("windows shim does not read the pin with set /p:\n%s", content)
	}
}

// Delayed expansion eats "!" from anything the parser expands, which would
// corrupt both the working directory and the arguments on the way to the
// toolchain, so it may only be on inside the validation subroutine.
func TestWindowsShim_LeavesDelayedExpansionOffInMainBody(t *testing.T) {
	content := (&Manager{root: `C:\fgm`, log: func(string, ...any) {}}).windowsShim("go")

	body, routine, found := strings.Cut(content, ":fgm_clean\r\n")
	if !found {
		t.Fatal("windows shim has no validation subroutine")
	}
	if strings.Contains(body, "enabledelayedexpansion") {
		t.Errorf("delayed expansion is enabled in the main body:\n%s", body)
	}
	if !strings.Contains(routine, "enabledelayedexpansion") {
		t.Errorf("validation subroutine does not enable delayed expansion:\n%s", routine)
	}
}
