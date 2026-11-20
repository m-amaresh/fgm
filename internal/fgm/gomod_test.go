package fgm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareGoVersions(t *testing.T) {
	// Each entry sorts strictly after the one before it.
	ordered := []string{"1.20", "1.20.1", "1.21", "1.21beta1", "1.21rc1", "1.21rc2", "1.21.0", "1.21.9", "1.21.10", "1.22", "2.0"}
	for i := 1; i < len(ordered); i++ {
		lo, hi := ordered[i-1], ordered[i]
		if c := compareGoVersions(lo, hi); c != -1 {
			t.Errorf("compareGoVersions(%q, %q) = %d, want -1", lo, hi, c)
		}
		if c := compareGoVersions(hi, lo); c != 1 {
			t.Errorf("compareGoVersions(%q, %q) = %d, want 1", hi, lo, c)
		}
	}
	if c := compareGoVersions("1.25.5", "1.25.5"); c != 0 {
		t.Errorf("equal versions compared %d", c)
	}
}

func TestParseGoVersion_RejectsGarbage(t *testing.T) {
	for _, v := range []string{"", "1", "1.", ".1", "1.x", "1.21.x", "1.21rc", "1.21beta", "1.21rcx", "v1.21.0", "1.21.0.1", "1.-1"} {
		if _, ok := parseGoVersion(v); ok {
			t.Errorf("parseGoVersion(%q) accepted", v)
		}
	}
}

func TestParseModRequirement(t *testing.T) {
	tests := []struct {
		name, data, want string
	}{
		{"go line only", "module x\n\ngo 1.25.0\n", "1.25.0"},
		{"language version", "module x\ngo 1.25\n", "1.25"},
		{"toolchain newer than go", "module x\ngo 1.25.0\ntoolchain go1.27.4\n", "1.27.4"},
		{"toolchain older than go", "module x\ngo 1.26.0\ntoolchain go1.25.0\n", "1.26.0"},
		{"toolchain default", "module x\ngo 1.25.0\ntoolchain default\n", "1.25.0"},
		{"custom toolchain suffix", "module x\ngo 1.25.0\ntoolchain go1.27.4-custom\n", "1.27.4"},
		{"trailing comment", "module x\ngo 1.25.0 // bumped for iterators\n", "1.25.0"},
		{"crlf", "module x\r\ngo 1.25.0\r\n", "1.25.0"},
		{"no go line", "module x\n\nrequire example.com/y v1.0.0\n", ""},
		{"garbage go line", "module x\ngo banana\n", ""},
		{"require block is ignored", "module x\ngo 1.25.0\nrequire (\n\tgolang.org/x/sys v0.50.0\n)\n", "1.25.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseModRequirement(tt.data); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindModRequirement_NearestGoMod(t *testing.T) {
	t.Setenv("GOWORK", "")
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module outer\ngo 1.24.0\n")
	writeFile(t, filepath.Join(root, "inner", "go.mod"), "module inner\ngo 1.26.1\n")
	deep := filepath.Join(root, "inner", "cmd", "app")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	req, err := FindModRequirement(deep)
	if err != nil {
		t.Fatal(err)
	}
	if req.Version != "1.26.1" || req.Path != filepath.Join(root, "inner", "go.mod") {
		t.Errorf("got %+v, want 1.26.1 from inner/go.mod", req)
	}
}

func TestFindModRequirement_NoModule(t *testing.T) {
	t.Setenv("GOWORK", "")
	req, err := FindModRequirement(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if req != (ModRequirement{}) {
		t.Errorf("got %+v outside a module", req)
	}
}

func TestFindModRequirement_WorkspaceWins(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.work"), "go 1.27.0\n\nuse ./mod\n")
	writeFile(t, filepath.Join(root, "mod", "go.mod"), "module mod\ngo 1.24.0\n")
	dir := filepath.Join(root, "mod")

	t.Setenv("GOWORK", "")
	req, err := FindModRequirement(dir)
	if err != nil {
		t.Fatal(err)
	}
	if req.Version != "1.27.0" || filepath.Base(req.Path) != "go.work" {
		t.Errorf("default GOWORK: got %+v, want the go.work", req)
	}

	t.Setenv("GOWORK", "off")
	if req, _ = FindModRequirement(dir); req.Version != "1.24.0" {
		t.Errorf("GOWORK=off: got %+v, want the go.mod", req)
	}

	explicit := filepath.Join(t.TempDir(), "other.work")
	writeFile(t, explicit, "go 1.28.0\n")
	t.Setenv("GOWORK", explicit)
	if req, _ = FindModRequirement(dir); req.Version != "1.28.0" || req.Path != explicit {
		t.Errorf("explicit GOWORK: got %+v, want %s", req, explicit)
	}
}

func TestModToolchainNote(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOENV", "off")
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\ngo 1.26.0\ntoolchain go1.27.4\n")

	tests := []struct {
		name, toolchain, active, want string
	}{
		{"satisfied", "", "1.27.4", ""},
		{"newer active", "", "1.28.0", ""},
		{"no active version", "", "", ""},
		{"auto switches", "", "1.25.5", ToolchainSwitches},
		{"explicit auto", "auto", "1.25.5", ToolchainSwitches},
		{"local+auto switches", "local+auto", "1.25.5", ToolchainSwitches},
		{"local fails", "local", "1.25.5", ToolchainFails},
		{"local+path searches", "local+path", "1.25.5", ToolchainSearchesPath},
		{"path searches", "path", "1.25.5", ToolchainSearchesPath},
		{"pin says nothing", "go1.26.0", "1.25.5", ""},
		{"pre-switching release", "", "1.20.14", ToolchainTooOld},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOTOOLCHAIN", tt.toolchain)
			note, err := ModToolchainNote(dir, tt.active)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if note != nil {
				got = note.Outcome
				if !strings.Contains(note.String(), "1.27.4") || !strings.Contains(note.String(), tt.active) {
					t.Errorf("message should name both versions: %q", note)
				}
			}
			if got != tt.want {
				t.Errorf("outcome = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDoctor_WarnsWhenGoModNeedsNewerGo(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "")
	m := newTestManager(t, "1.25.5")
	if err := m.Use(t.Context(), "1.25.5"); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	t.Chdir(work)
	if hasDoctorCheckNamed(m.Doctor(), "go.mod") {
		t.Error("doctor should say nothing about go.mod outside a module")
	}

	writeFile(t, filepath.Join(work, "go.mod"), "module x\ngo 1.25.0\n")
	if !hasDoctorCheck(m.Doctor(), "go.mod", DoctorOK) {
		t.Error("expected a satisfied go.mod to be reported ok")
	}

	writeFile(t, filepath.Join(work, "go.mod"), "module x\ngo 1.27.4\n")
	if !hasDoctorCheck(m.Doctor(), "go.mod", DoctorWarn) {
		t.Error("expected a go.mod needing newer Go to warn")
	}
}

func hasDoctorCheckNamed(checks []DoctorCheck, name string) bool {
	for _, c := range checks {
		if c.Name == name {
			return true
		}
	}
	return false
}
