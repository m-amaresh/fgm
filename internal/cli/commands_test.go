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

func TestCurrentCommand_NoActiveVersion(t *testing.T) {
	output, err := runCLICommand(t, newCurrentCmd())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "no active Go version" {
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

// runAvailableCommand builds a fresh available command so the --all flag
// defined on it is in scope when RunE looks it up via cmd.Flags().GetBool.
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
