package fgm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

type DoctorCheck struct {
	Status  string
	Name    string
	Message string
}

func (m *Manager) Doctor() []DoctorCheck {
	checks := []DoctorCheck{
		m.checkDir("fgm root", m.root),
		m.checkDir("versions directory", m.versionsDir()),
		m.checkDir("shim directory", m.binDir()),
		m.checkBinOnPath(),
	}

	effective, err := m.EffectiveCwd()
	switch {
	case err != nil:
		checks = append(checks, DoctorCheck{
			Status:  DoctorFail,
			Name:    "active version",
			Message: err.Error(),
		})
	case effective.Version == "":
		checks = append(checks, DoctorCheck{
			Status:  DoctorWarn,
			Name:    "active version",
			Message: "no active Go version; run: fgm use latest",
		})
	case effective.IsLocal():
		checks = append(checks, DoctorCheck{
			Status:  DoctorOK,
			Name:    "active version",
			Message: fmt.Sprintf("%s (pinned by %s)", effective.Version, effective.Path),
		})
	default:
		checks = append(checks, DoctorCheck{
			Status:  DoctorOK,
			Name:    "active version",
			Message: effective.Version,
		})
	}

	if err == nil {
		if check, ok := checkModToolchain(effective.Version); ok {
			checks = append(checks, check)
		}
	}

	for _, name := range shimNames {
		checks = append(checks, m.checkShim(name))
	}

	return append(checks, checkGoToolchain())
}

// checkGoToolchain reports the GOTOOLCHAIN setting. Go can switch toolchains
// on its own, so a value naming a specific toolchain silently overrides
// whatever version fgm activated — that is worth flagging.
func checkGoToolchain() DoctorCheck {
	setting := goToolchain()
	switch {
	case setting.IsPin():
		return DoctorCheck{
			Status:  DoctorWarn,
			Name:    "GOTOOLCHAIN",
			Message: fmt.Sprintf("pinned to %s; this overrides the version fgm activates", setting),
		}
	case setting.Source == "":
		return DoctorCheck{
			Status:  DoctorOK,
			Name:    "GOTOOLCHAIN",
			Message: setting.String() + "; a go.mod needing a newer Go will download and run that toolchain",
		}
	default:
		return DoctorCheck{Status: DoctorOK, Name: "GOTOOLCHAIN", Message: setting.String()}
	}
}

// checkModToolchain compares the version fgm resolves to against what the
// go.mod or go.work in scope asks for. It reports nothing outside a module.
func checkModToolchain(active string) (DoctorCheck, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return DoctorCheck{}, false
	}
	note, err := ModToolchainNote(dir, active)
	switch {
	case err != nil:
		return DoctorCheck{Status: DoctorWarn, Name: "go.mod", Message: err.Error()}, true
	case note != nil:
		return DoctorCheck{Status: DoctorWarn, Name: "go.mod", Message: note.String()}, true
	}
	req, err := FindModRequirement(dir)
	if err != nil || req.Version == "" || active == "" {
		return DoctorCheck{}, false
	}
	return DoctorCheck{
		Status:  DoctorOK,
		Name:    "go.mod",
		Message: fmt.Sprintf("%s requires go %s; satisfied by %s", req.Path, req.Version, active),
	}, true
}

func (m *Manager) checkDir(name, path string) DoctorCheck {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return DoctorCheck{
			Status:  DoctorWarn,
			Name:    name,
			Message: fmt.Sprintf("%s does not exist yet", path),
		}
	}
	if err != nil {
		return DoctorCheck{
			Status:  DoctorFail,
			Name:    name,
			Message: fmt.Sprintf("cannot stat %s: %v", path, err),
		}
	}
	if !info.IsDir() {
		return DoctorCheck{
			Status:  DoctorFail,
			Name:    name,
			Message: fmt.Sprintf("%s is not a directory", path),
		}
	}
	return DoctorCheck{Status: DoctorOK, Name: name, Message: path}
}

func (m *Manager) checkBinOnPath() DoctorCheck {
	if pathContainsDir(os.Getenv("PATH"), m.binDir()) {
		return DoctorCheck{
			Status:  DoctorOK,
			Name:    "PATH",
			Message: fmt.Sprintf("%s is on PATH", m.binDir()),
		}
	}
	return DoctorCheck{
		Status:  DoctorWarn,
		Name:    "PATH",
		Message: fmt.Sprintf("%s is not on PATH", m.binDir()),
	}
}

func (m *Manager) checkShim(name string) DoctorCheck {
	path := filepath.Join(m.binDir(), shimFilename(name))
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return DoctorCheck{
			Status:  DoctorWarn,
			Name:    name + " shim",
			Message: fmt.Sprintf("%s does not exist; run: fgm use latest", path),
		}
	}
	if err != nil {
		return DoctorCheck{
			Status:  DoctorFail,
			Name:    name + " shim",
			Message: fmt.Sprintf("cannot stat %s: %v", path, err),
		}
	}
	if info.IsDir() {
		return DoctorCheck{
			Status:  DoctorFail,
			Name:    name + " shim",
			Message: fmt.Sprintf("%s is a directory", path),
		}
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return DoctorCheck{
			Status:  DoctorFail,
			Name:    name + " shim",
			Message: fmt.Sprintf("%s is not executable", path),
		}
	}

	// Upgrading fgm replaces the binary but leaves the shims alone, so a shim
	// written by an older version can quietly keep resolving versions the old
	// way — ignoring .go-version, for instance.
	current, err := m.shimIsCurrent(name)
	if err != nil {
		return DoctorCheck{
			Status:  DoctorFail,
			Name:    name + " shim",
			Message: fmt.Sprintf("cannot read %s: %v", path, err),
		}
	}
	if !current {
		return DoctorCheck{
			Status:  DoctorWarn,
			Name:    name + " shim",
			Message: fmt.Sprintf("%s is out of date; run: fgm reshim", path),
		}
	}
	return DoctorCheck{Status: DoctorOK, Name: name + " shim", Message: path}
}

func shimFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".cmd"
	}
	return name
}

func pathContainsDir(pathValue, dir string) bool {
	dir = cleanPathForCompare(dir)
	for _, entry := range filepath.SplitList(pathValue) {
		if cleanPathForCompare(entry) == dir {
			return true
		}
	}
	return false
}

func cleanPathForCompare(path string) string {
	cleaned := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
