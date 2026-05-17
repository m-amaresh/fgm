package fgm

import (
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

	current, err := m.Current()
	switch {
	case err != nil:
		checks = append(checks, DoctorCheck{
			Status:  DoctorFail,
			Name:    "active version",
			Message: err.Error(),
		})
	case current == "":
		checks = append(checks, DoctorCheck{
			Status:  DoctorWarn,
			Name:    "active version",
			Message: "no active Go version; run: fgm use latest",
		})
	default:
		checks = append(checks, DoctorCheck{
			Status:  DoctorOK,
			Name:    "active version",
			Message: current,
		})
	}

	for _, name := range []string{"go", "gofmt"} {
		checks = append(checks, m.checkShim(name))
	}

	return checks
}

func (m *Manager) checkDir(name, path string) DoctorCheck {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
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
	if os.IsNotExist(err) {
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
