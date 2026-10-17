package fgm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ResolveVersion resolves "latest", minor versions (e.g. "1.25"), and exact
// versions to a concrete Go version string. The result is always a full
// version like "1.25.5".
func (m *Manager) ResolveVersion(ctx context.Context, input string) (string, error) {
	if input == "latest" {
		releases, err := m.manifest(ctx)
		if err != nil {
			return "", err
		}
		// Skip RC/beta releases — stable versions contain only digits and dots
		// after the "go" prefix.
		for _, r := range releases {
			v := strings.TrimPrefix(r.Version, "go")
			if isStableVersion(v) {
				return v, nil
			}
		}
		return "", errors.New("no stable releases found in Go manifest")
	}

	// If it looks like a full version (has two dots), validate and return.
	if strings.Count(input, ".") >= 2 {
		if err := validateVersion(input); err != nil {
			return "", err
		}
		return input, nil
	}

	// Minor version like "1.25" → find latest stable patch.
	if err := validateVersion(input); err != nil {
		return "", err
	}
	releases, err := m.manifest(ctx)
	if err != nil {
		return "", err
	}
	prefix := "go" + input
	for _, r := range releases {
		v := strings.TrimPrefix(r.Version, "go")
		if (r.Version == prefix || strings.HasPrefix(r.Version, prefix+".")) && isStableVersion(v) {
			return v, nil
		}
	}
	return "", fmt.Errorf("no release found matching %q", input)
}

// ResolveInstalledVersion resolves "latest", minor versions (e.g. "1.25"),
// and exact versions against locally installed toolchains only.
func (m *Manager) ResolveInstalledVersion(input string) (string, error) {
	if input == "latest" {
		versions, _, err := m.List()
		if err != nil {
			return "", err
		}
		if len(versions) == 0 {
			return "", errors.New("no Go versions are installed")
		}
		return versions[len(versions)-1], nil
	}

	if strings.Count(input, ".") >= 2 {
		if err := validateVersion(input); err != nil {
			return "", err
		}
		if !m.isInstalled(input) {
			return "", m.notInstalledError(input)
		}
		return input, nil
	}

	if err := validateVersion(input); err != nil {
		return "", err
	}
	versions, _, err := m.List()
	if err != nil {
		return "", err
	}
	prefix := input + "."
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i] == input || strings.HasPrefix(versions[i], prefix) {
			return versions[i], nil
		}
	}
	return "", fmt.Errorf("no installed version found matching %q", input)
}

// validateMarkerVersion accepts exact release names. Before Go 1.21 the
// initial release was named 1.N, rather than 1.N.0. Modern minor versions
// remain invalid here because the shims do no version resolution.
func validateMarkerVersion(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if strings.Count(version, ".") != 2 {
		major, minor, _ := strings.Cut(version, ".")
		n, err := strconv.Atoi(minor)
		if err == nil && major == "1" && n >= 0 && n <= 20 && strconv.Itoa(n) == minor {
			return nil
		}
		return fmt.Errorf("invalid Go version %q (expected an exact version like 1.25.5)", version)
	}
	return nil
}

func validateVersion(version string) error {
	if version == "" {
		return errors.New("version cannot be empty")
	}
	parts := strings.Split(version, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("invalid Go version %q", version)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid Go version %q", version)
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return fmt.Errorf("invalid Go version %q", version)
			}
		}
	}
	return nil
}

// isStableVersion returns true if version contains only digits and dots
// (i.e. no "rc", "beta", etc.).
func isStableVersion(version string) bool {
	for _, r := range version {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return version != ""
}
