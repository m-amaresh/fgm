package fgm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ModRequirement is the minimum Go toolchain a module or workspace asks for:
// the higher of its go and toolchain lines.
type ModRequirement struct {
	// Version is a Go version without the "go" prefix, e.g. "1.27.4" or
	// "1.27" — a go line may name a language version with no patch.
	Version string
	// Path is the go.mod or go.work file the requirement came from.
	Path string
}

// FindModRequirement finds the go.work or go.mod that the go command would
// read when run from dir and returns the toolchain it requires. A zero
// ModRequirement means there is no such file or it names no version.
//
// A workspace wins over the module, as in cmd/go: GOWORK=off disables the
// workspace, an explicit GOWORK path is used as-is, and otherwise the nearest
// go.work above dir applies.
func FindModRequirement(dir string) (ModRequirement, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ModRequirement{}, fmt.Errorf("resolve %s: %w", dir, err)
	}

	switch gowork := os.Getenv("GOWORK"); gowork {
	case "off":
	case "", "auto":
		if path, err := findUp(dir, "go.work"); err != nil {
			return ModRequirement{}, err
		} else if path != "" {
			return readModRequirement(path)
		}
	default:
		return readModRequirement(gowork)
	}

	path, err := findUp(dir, "go.mod")
	if err != nil || path == "" {
		return ModRequirement{}, err
	}
	return readModRequirement(path)
}

// findUp returns the nearest file called name in dir or one of its parents,
// or "" when there is none.
func findUp(dir, name string) (string, error) {
	for {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", candidate, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func readModRequirement(path string) (ModRequirement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ModRequirement{}, fmt.Errorf("read %s: %w", path, err)
	}
	version := parseModRequirement(string(data))
	if version == "" {
		return ModRequirement{}, nil
	}
	return ModRequirement{Version: version, Path: path}, nil
}

// parseModRequirement extracts the minimum toolchain from go.mod or go.work
// contents: the higher of the go and toolchain lines. Neither directive can
// appear in a block, so a line scan is enough; anything unparseable is
// ignored rather than guessed at, since the go command reports bad files far
// better than fgm could.
func parseModRequirement(data string) string {
	var required string
	for line := range strings.Lines(data) {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		var v string
		switch fields[0] {
		case "go":
			v = fields[1]
		case "toolchain":
			// "toolchain default" names no toolchain. A custom build may add
			// a suffix (go1.27.4-custom, go1.27.4+auto) that does not change
			// which release it is.
			name, ok := strings.CutPrefix(fields[1], "go")
			if !ok {
				continue
			}
			v, _, _ = strings.Cut(name, "-")
			v, _, _ = strings.Cut(v, "+")
		default:
			continue
		}
		if _, ok := parseGoVersion(v); !ok {
			continue
		}
		if required == "" || compareGoVersions(v, required) > 0 {
			required = v
		}
	}
	return required
}

// goVersion is a parsed Go toolchain or language version.
type goVersion struct {
	major, minor int
	// kind orders the forms within one minor version the way cmd/go does:
	// the bare language version 1.21 sorts before 1.21beta1, which sorts
	// before 1.21rc1, which sorts before the release 1.21.0.
	kind int
	// n is the beta/rc number or the patch number, depending on kind.
	n int
}

const (
	goVersionLang = iota
	goVersionBeta
	goVersionRC
	goVersionRelease
)

func parseGoVersion(v string) (goVersion, bool) {
	majorStr, rest, ok := strings.Cut(v, ".")
	if !ok {
		return goVersion{}, false
	}
	major, ok := parseVersionNumber(majorStr)
	if !ok {
		return goVersion{}, false
	}

	minorStr, patchStr, hasPatch := strings.Cut(rest, ".")
	kind, n := goVersionLang, 0
	switch {
	case hasPatch:
		patch, ok := parseVersionNumber(patchStr)
		if !ok {
			return goVersion{}, false
		}
		kind, n = goVersionRelease, patch
	case strings.Contains(minorStr, "rc"):
		var pre string
		minorStr, pre, _ = strings.Cut(minorStr, "rc")
		if n, ok = parseVersionNumber(pre); !ok {
			return goVersion{}, false
		}
		kind = goVersionRC
	case strings.Contains(minorStr, "beta"):
		var pre string
		minorStr, pre, _ = strings.Cut(minorStr, "beta")
		if n, ok = parseVersionNumber(pre); !ok {
			return goVersion{}, false
		}
		kind = goVersionBeta
	}
	minor, ok := parseVersionNumber(minorStr)
	if !ok {
		return goVersion{}, false
	}
	return goVersion{major: major, minor: minor, kind: kind, n: n}, true
}

func parseVersionNumber(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// compareGoVersions orders Go versions the way cmd/go does, including
// language versions (1.21) and prereleases (1.21rc1). Unparseable versions
// sort lowest.
func compareGoVersions(a, b string) int {
	va, okA := parseGoVersion(a)
	vb, okB := parseGoVersion(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for _, d := range [...]int{va.major - vb.major, va.minor - vb.minor, va.kind - vb.kind, va.n - vb.n} {
		if d != 0 {
			if d < 0 {
				return -1
			}
			return 1
		}
	}
	return 0
}

// firstSwitchingRelease is the first Go release that can download and run a
// toolchain a go.mod asks for. Older releases just try to build.
const firstSwitchingRelease = "1.21"

// ToolchainNote explains what the go command will do when the module or
// workspace in scope needs a newer Go than the version fgm resolves to.
type ToolchainNote struct {
	Required ModRequirement
	Active   string
	// Outcome is one of the ToolchainSwitches, ToolchainFails, ... constants.
	Outcome string
}

const (
	// ToolchainSwitches: go downloads and runs a newer toolchain.
	ToolchainSwitches = "switches"
	// ToolchainSearchesPath: go looks for a goX.Y.Z binary on PATH.
	ToolchainSearchesPath = "path"
	// ToolchainFails: go refuses to run commands in this module.
	ToolchainFails = "fails"
	// ToolchainTooOld: the active Go predates toolchain switching.
	ToolchainTooOld = "too-old"
)

// String renders the note as a single sentence for CLI output.
func (n ToolchainNote) String() string {
	head := fmt.Sprintf("%s requires go %s, but the active version is %s",
		n.Required.Path, n.Required.Version, n.Active)
	switch n.Outcome {
	case ToolchainSwitches:
		return head + "; go will download and run a newer toolchain instead (run: fgm use " + n.Required.Version + " --local to match)"
	case ToolchainSearchesPath:
		return head + "; with GOTOOLCHAIN=" + goToolchain().Value + ", go will look for a newer toolchain on PATH and fail if there is none"
	case ToolchainFails:
		return head + "; with GOTOOLCHAIN=" + goToolchain().Value + ", go commands here will fail"
	default:
		return head + "; go " + n.Active + " predates toolchain switching, so builds here may fail"
	}
}

// ModToolchainNote reports whether the go.mod or go.work in scope for dir
// asks for a newer Go than active, and what the go command will do about it.
// It returns nil when there is nothing to say: no module, an old enough
// requirement, no active version, or a GOTOOLCHAIN pin — doctor flags the pin
// on its own, and with one in place fgm's version is not what runs anyway.
func ModToolchainNote(dir, active string) (*ToolchainNote, error) {
	if active == "" {
		return nil, nil
	}
	req, err := FindModRequirement(dir)
	if err != nil || req.Version == "" {
		return nil, err
	}
	if compareGoVersions(req.Version, active) <= 0 {
		return nil, nil
	}

	note := &ToolchainNote{Required: req, Active: active}
	setting := goToolchain()
	base, suffix, _ := strings.Cut(setting.Value, "+")
	switch {
	case compareGoVersions(active, firstSwitchingRelease) < 0:
		note.Outcome = ToolchainTooOld
	case setting.IsPin():
		return nil, nil
	case base == "auto" || suffix == "auto":
		note.Outcome = ToolchainSwitches
	case base == "path" || suffix == "path":
		note.Outcome = ToolchainSearchesPath
	default:
		note.Outcome = ToolchainFails
	}
	return note, nil
}
