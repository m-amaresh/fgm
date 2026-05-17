package fgm

import "runtime"

type EnvInfo struct {
	FGMDir         string
	ShimDir        string
	VersionsDir    string
	DownloadsDir   string
	CurrentVersion string
	CurrentError   string
	Platform       string
}

func (m *Manager) Env() EnvInfo {
	current, err := m.Current()
	info := EnvInfo{
		FGMDir:       m.root,
		ShimDir:      m.binDir(),
		VersionsDir:  m.versionsDir(),
		DownloadsDir: m.downloadsDir(),
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
	}
	if err != nil {
		info.CurrentError = err.Error()
		return info
	}
	info.CurrentVersion = current
	return info
}
