package fgm

import (
	"context"
	"slices"
	"strings"
)

func (m *Manager) Available(ctx context.Context, all bool) ([]string, error) {
	releases, err := m.manifest(ctx)
	if err != nil {
		return nil, err
	}
	osName, arch, ext, err := platformReleaseParts()
	if err != nil {
		return nil, err
	}

	versions := make([]string, 0, len(releases))
	for _, release := range releases {
		version := strings.TrimPrefix(release.Version, "go")
		if !isStableVersion(version) || !releaseHasPlatformArchive(release, osName, arch, ext) {
			continue
		}
		versions = append(versions, version)
	}
	slices.SortFunc(versions, func(a, b string) int {
		return compareVersions(b, a)
	})
	if all {
		return versions, nil
	}

	seenMinor := make(map[string]bool)
	latestPerMinor := make([]string, 0, len(versions))
	for _, version := range versions {
		key := minorVersionKey(version)
		if seenMinor[key] {
			continue
		}
		seenMinor[key] = true
		latestPerMinor = append(latestPerMinor, version)
	}
	return latestPerMinor, nil
}

func releaseHasPlatformArchive(release releaseManifest, osName, arch, ext string) bool {
	for _, file := range release.Files {
		if file.Kind != "archive" {
			continue
		}
		if file.OS == osName && file.Arch == arch && strings.HasSuffix(file.Filename, ext) {
			return true
		}
	}
	return false
}

func minorVersionKey(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}
