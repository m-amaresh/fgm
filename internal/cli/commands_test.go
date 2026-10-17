package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/m-amaresh/fgm/internal/fgm"
)

// TestMain runs the package from a scratch directory: several commands
// resolve a .go-version by walking up from the working directory, and a stray
// pin above the checkout would otherwise change results.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fgm-cli-cwd-")
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestCurrentCommand_NoActiveVersion(t *testing.T) {
	output, err := runCLICommand(t, newCurrentCmd())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "No active Go version. Run: fgm use latest" {
		t.Fatalf("current output = %q", output)
	}
}

func TestListCommand_NoInstalledVersions(t *testing.T) {
	output, err := runCLICommand(t, newListCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "No Go versions installed. Run: fgm install latest") {
		t.Fatalf("list output = %q", output)
	}
}

func TestEnvCommand_Output(t *testing.T) {
	output, err := runCLICommand(t, newEnvCmd())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FGM_DIR:", "shim dir:", "versions dir:", "downloads dir:", "current:", "platform:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("env output missing %q: %q", want, output)
		}
	}
}

func TestDoctorCommand_Output(t *testing.T) {
	output, err := runCLICommand(t, newDoctorCmd())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fgm root", "versions directory", "shim directory", "PATH", "active version", "go shim", "gofmt shim"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q: %q", want, output)
		}
	}
}

func TestAvailableCommand_FlagsAndAlias(t *testing.T) {
	cmd := newAvailableCmd()
	if cmd.Flags().Lookup("all") == nil {
		t.Fatal("--all flag not registered on available command")
	}
	if !slices.Contains(cmd.Aliases, "list-remote") {
		t.Fatalf("list-remote alias missing; got %v", cmd.Aliases)
	}
}

func TestAvailableCommand_OutputUsesCachedManifest(t *testing.T) {
	osName, arch, ext := currentPlatformParts(t)
	root := t.TempDir()
	writeManifestCache(t, root, []releaseStub{
		{version: "go1.25.5", osName: osName, arch: arch, ext: ext},
		{version: "go1.25.4", osName: osName, arch: arch, ext: ext},
		{version: "go1.24.10", osName: osName, arch: arch, ext: ext},
	})

	manager, err := fgm.NewManager(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runAvailableCommand(t, manager, false)
	if err != nil {
		t.Fatal(err)
	}
	// Default (no --all): one entry per minor (latest patch only).
	if !strings.Contains(out, "1.25.5") {
		t.Fatalf("default output missing 1.25.5: %q", out)
	}
	if strings.Contains(out, "1.25.4") {
		t.Fatalf("default output should hide older patch 1.25.4: %q", out)
	}
	if !strings.Contains(out, "1.24.10") {
		t.Fatalf("default output missing 1.24.10: %q", out)
	}

	// --all: every stable patch.
	outAll, err := runAvailableCommand(t, manager, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1.25.5", "1.25.4", "1.24.10"} {
		if !strings.Contains(outAll, want) {
			t.Fatalf("--all output missing %s: %q", want, outAll)
		}
	}
}

func TestCommandMissingManagerReturnsError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := newCurrentCmd().RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "manager not initialized") {
		t.Fatalf("expected missing manager error, got %v", err)
	}
}

// runAvailableCommand builds a fresh available command so ParseFlags sets the
// --all variable bound to that command's RunE closure.
func runAvailableCommand(t *testing.T, manager *fgm.Manager, all bool) (string, error) {
	t.Helper()
	cmd := newAvailableCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.WithValue(context.Background(), managerKey{}, manager))
	args := []string{}
	if all {
		args = append(args, "--all")
	}
	if err := cmd.ParseFlags(args); err != nil {
		return out.String(), err
	}
	err := cmd.RunE(cmd, nil)
	return out.String(), err
}

type releaseStub struct {
	version string
	osName  string
	arch    string
	ext     string
}

func writeManifestCache(t *testing.T, root string, stubs []releaseStub) {
	t.Helper()
	type fileEntry struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Version  string `json:"version"`
		SHA256   string `json:"sha256"`
		Kind     string `json:"kind"`
	}
	type release struct {
		Version string      `json:"version"`
		Files   []fileEntry `json:"files"`
	}
	type cache struct {
		FetchedAt time.Time `json:"fetched_at"`
		Releases  []release `json:"releases"`
	}
	releases := make([]release, 0, len(stubs))
	for _, s := range stubs {
		releases = append(releases, release{
			Version: s.version,
			Files: []fileEntry{{
				Filename: s.version + "." + s.osName + "-" + s.arch + s.ext,
				OS:       s.osName,
				Arch:     s.arch,
				Version:  s.version,
				Kind:     "archive",
			}},
		})
	}
	data, err := json.Marshal(cache{FetchedAt: time.Now(), Releases: releases})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest-cache.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func currentPlatformParts(t *testing.T) (string, string, string) {
	t.Helper()
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux", "amd64", ".tar.gz"
	case "linux/arm64":
		return "linux", "arm64", ".tar.gz"
	case "darwin/arm64":
		return "darwin", "arm64", ".tar.gz"
	case "windows/amd64":
		return "windows", "amd64", ".zip"
	case "windows/arm64":
		return "windows", "arm64", ".zip"
	}
	t.Skipf("unsupported test platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	return "", "", ""
}

// newCLIManager builds a manager whose root already contains a cached
// manifest and the named versions, so command bodies can be exercised end to
// end without touching the network: Install short-circuits on an installed
// version, and exact versions resolve without a manifest lookup at all.
func newCLIManager(t *testing.T, versions ...string) *fgm.Manager {
	t.Helper()
	root := t.TempDir()

	osName, arch, ext := currentPlatformParts(t)
	stubs := make([]releaseStub, 0, len(versions))
	for _, v := range versions {
		stubs = append(stubs, releaseStub{version: "go" + v, osName: osName, arch: arch, ext: ext})
	}
	writeManifestCache(t, root, stubs)

	for _, v := range versions {
		binDir := filepath.Join(root, "versions", v, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	manager, err := fgm.NewManager(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

// runCmd parses args against command and runs it, returning captured output.
func runCmd(t *testing.T, manager *fgm.Manager, command *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	command.SetContext(context.WithValue(context.Background(), managerKey{}, manager))
	if err := command.ParseFlags(args); err != nil {
		return out.String(), err
	}
	err := command.RunE(command, command.Flags().Args())
	return out.String(), err
}

func TestVersionCommand_Output(t *testing.T) {
	out, err := runCmd(t, newCLIManager(t), newVersionCmd())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version:", "commit:", "built:", "go:", "os/arch:"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q: %q", want, out)
		}
	}
}

func TestInstallCommand_AlreadyInstalled(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")

	out, err := runCmd(t, manager, newInstallCmd(), "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already installed") {
		t.Errorf("expected an already-installed notice: %q", out)
	}
	if !strings.Contains(out, "To activate: fgm use 1.25.5") {
		t.Errorf("expected an activation hint: %q", out)
	}
}

func TestInstallCommand_UseFlagActivates(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")

	out, err := runCmd(t, manager, newInstallCmd(), "--use", "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Using go 1.25.5") {
		t.Errorf("expected --use to activate: %q", out)
	}
	if current, err := manager.Current(); err != nil || current != "1.25.5" {
		t.Errorf("Current() = %q, %v; want 1.25.5", current, err)
	}
}

func TestInstallCommand_ResolvesLatestFromManifest(t *testing.T) {
	manager := newCLIManager(t, "1.25.5", "1.24.0")

	out, err := runCmd(t, manager, newInstallCmd(), "latest")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1.25.5") {
		t.Errorf("expected latest to resolve to 1.25.5: %q", out)
	}
}

func TestUseCommand_ActivatesGlobally(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")

	out, err := runCmd(t, manager, newUseCmd(), "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Using go 1.25.5") {
		t.Errorf("use output = %q", out)
	}
	if current, err := manager.Current(); err != nil || current != "1.25.5" {
		t.Errorf("Current() = %q, %v; want 1.25.5", current, err)
	}
}

func TestUseCommand_LocalWritesPin(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")
	work := t.TempDir()
	t.Chdir(work)

	out, err := runCmd(t, manager, newUseCmd(), "--local", "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Pinned go 1.25.5") {
		t.Errorf("use --local output = %q", out)
	}

	data, err := os.ReadFile(filepath.Join(work, fgm.LocalVersionFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "1.25.5" {
		t.Errorf("%s = %q", fgm.LocalVersionFile, string(data))
	}
	// A local pin must not disturb the global marker.
	if current, _ := manager.Current(); current != "" {
		t.Errorf("expected no global version, got %q", current)
	}
}

func TestUseCommand_WarnsWhenPinShadowsActivation(t *testing.T) {
	manager := newCLIManager(t, "1.25.5", "1.24.0")
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile(filepath.Join(work, fgm.LocalVersionFile), []byte("1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, manager, newUseCmd(), "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "overrides this") || !strings.Contains(out, "1.24.0") {
		t.Errorf("expected a warning that the pin wins: %q", out)
	}
}

func TestCurrentCommand_ReportsPin(t *testing.T) {
	manager := newCLIManager(t, "1.25.5", "1.24.0")
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile(filepath.Join(work, fgm.LocalVersionFile), []byte("1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, manager, newCurrentCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1.24.0") || !strings.Contains(out, "pinned by") {
		t.Errorf("expected the pin to be reported: %q", out)
	}
}

func TestListCommand_MarksCurrent(t *testing.T) {
	manager := newCLIManager(t, "1.25.5", "1.24.0")
	if err := manager.Activate("1.25.5"); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, manager, newListCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1.25.5 (current)") {
		t.Errorf("expected 1.25.5 marked current: %q", out)
	}
	if !strings.Contains(out, "1.24.0") {
		t.Errorf("expected 1.24.0 listed: %q", out)
	}
}

func TestUninstallCommand_RemovesVersion(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")

	out, err := runCmd(t, manager, newUninstallCmd(), "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Uninstalled go 1.25.5") {
		t.Errorf("uninstall output = %q", out)
	}
	if manager.IsInstalled("1.25.5") {
		t.Error("version should be gone after uninstall")
	}
}

// Uninstalling clears the global marker, but fgm will not edit a project's
// .go-version — so it has to say that the pin is now broken.
func TestUninstallCommand_WarnsAboutLocalPin(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile(filepath.Join(work, fgm.LocalVersionFile), []byte("1.25.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, manager, newUninstallCmd(), "1.25.5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "still pins go 1.25.5") || !strings.Contains(out, fgm.LocalVersionFile) {
		t.Errorf("expected a warning naming the orphaned pin: %q", out)
	}
}

func TestUninstallCommand_NotInstalled(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")

	_, err := runCmd(t, manager, newUninstallCmd(), "1.19.0")
	if err == nil {
		t.Fatal("expected an error uninstalling a version that is not installed")
	}
	if !strings.Contains(err.Error(), "1.19.0") {
		t.Errorf("error should name the version: %v", err)
	}
}

func TestPruneCommand(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")

	// The seeded manifest cache is prunable, so the first run clears it.
	out, err := runCmd(t, manager, newPruneCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Removed 1 cached file(s)") {
		t.Errorf("expected the manifest cache to be pruned: %q", out)
	}

	downloads := filepath.Join(manager.Root(), "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "go1.25.5.tar.gz"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = runCmd(t, manager, newPruneCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Removed 1 cached file(s)") {
		t.Errorf("expected the cached archive to be pruned: %q", out)
	}

	out, err = runCmd(t, manager, newPruneCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing to prune.") {
		t.Errorf("expected nothing left to prune: %q", out)
	}
}

func TestEnvCommand_ReportsPinAndToolchain(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile(filepath.Join(work, fgm.LocalVersionFile), []byte("1.25.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, manager, newEnvCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "local:") || !strings.Contains(out, fgm.LocalVersionFile) {
		t.Errorf("expected the pin reported in env: %q", out)
	}
	if !strings.Contains(out, "GOTOOLCHAIN:") {
		t.Errorf("expected GOTOOLCHAIN in env: %q", out)
	}
}

// A .go-version the shims will refuse must not be reported as "no pin here":
// env is where people look when go and fgm disagree.
func TestEnvCommand_WarnsAboutUnusablePin(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile(filepath.Join(work, fgm.LocalVersionFile), []byte("../../elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, newCLIManager(t), newEnvCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, fgm.LocalVersionFile) {
		t.Errorf("expected a warning naming the unusable pin: %q", out)
	}
}

func TestReshimCommand_RewritesShims(t *testing.T) {
	manager := newCLIManager(t, "1.25.5")
	shim := filepath.Join(manager.ShimDir(), shimName("go"))
	if err := os.MkdirAll(manager.ShimDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("stale shim from an older fgm\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, manager, newReshimCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Shims regenerated") {
		t.Errorf("reshim output = %q", out)
	}
	content, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "current-version") {
		t.Errorf("shim was not rewritten: %q", content)
	}
}

func shimName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".cmd"
	}
	return name
}

func TestDoctorCommand_ReportsGoToolchain(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "go1.21.0")

	out, err := runCmd(t, newCLIManager(t), newDoctorCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "GOTOOLCHAIN") || !strings.Contains(out, "overrides") {
		t.Errorf("expected a warning about the pinned toolchain: %q", out)
	}
}

func runCLICommand(t *testing.T, command *cobra.Command) (string, error) {
	t.Helper()
	manager, err := fgm.NewManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.WithValue(context.Background(), managerKey{}, manager))
	err = command.RunE(cmd, nil)
	return out.String(), err
}
