# FGM – Fast Go Manager installer for Windows
# Usage: iwr -useb https://raw.githubusercontent.com/m-amaresh/fgm/main/scripts/install.ps1 | iex
$ErrorActionPreference = "Stop"

# Require PowerShell 5.1+ (ships with Windows 10+).
if ($PSVersionTable.PSVersion.Major -lt 5 -or
    ($PSVersionTable.PSVersion.Major -eq 5 -and $PSVersionTable.PSVersion.Minor -lt 1)) {
    throw "FGM requires PowerShell 5.1 or later. Current: $($PSVersionTable.PSVersion)"
}

$Repo = "m-amaresh/fgm"
$FgmDir = "$env:USERPROFILE\.fgm"
$InstallDir = "$FgmDir\bin"

function Get-LatestVersion {
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
        return $release.tag_name
    } catch {
        throw "Failed to fetch latest FGM release from GitHub: $_"
    }
}

function Get-Arch {
    # Try .NET RuntimeInformation first (PS 6+ / .NET Core, and .NET 4.7.1+).
    try {
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
        switch ("$arch") {
            "X64"   { return "amd64" }
            "Arm64" { return "arm64" }
        }
    } catch {}

    # Fallback: PROCESSOR_ARCHITECTURE environment variable (always available).
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
    }
}

# ── main ─────────────────────────────────────────────────────────────
$Arch = Get-Arch
$Version = Get-LatestVersion
$VersionNum = $Version

Write-Host "=> Installing FGM $Version (windows/$Arch)..." -ForegroundColor Cyan

$Archive = "fgm_${VersionNum}_windows_${Arch}.zip"
$Url = "https://github.com/$Repo/releases/download/$Version/$Archive"

$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("fgm-install-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null

try {
    $ZipPath = Join-Path $TmpDir $Archive
    Write-Host "=> Downloading $Archive..."
    Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing

    # Download and verify checksum
    $ChecksumsUrl = "https://github.com/$Repo/releases/download/$Version/checksums.txt"
    $ChecksumsPath = Join-Path $TmpDir "checksums.txt"
    Write-Host "=> Verifying checksum..."
    Invoke-WebRequest -Uri $ChecksumsUrl -OutFile $ChecksumsPath -UseBasicParsing

    $ChecksumLines = Get-Content $ChecksumsPath
    if (-not $ChecksumLines -or $ChecksumLines.Count -eq 0) {
        throw "checksums.txt is empty or missing"
    }
    # Validate format: each line should be 64-char hex hash + two spaces + filename.
    $ValidLines = $ChecksumLines | Where-Object { $_ -match '^[0-9a-f]{64}  .+' }
    if ($ValidLines.Count -eq 0) {
        throw "checksums.txt has unexpected format - possible tampering"
    }
    # Strict match: exact filename at end of line.
    $ChecksumLine = $ChecksumLines | Where-Object { $_ -match "^[0-9a-f]{64}  ${Archive}$" }
    if (-not $ChecksumLine) {
        throw "Checksum not found for $Archive in checksums.txt"
    }
    if ($ChecksumLine -is [array] -and $ChecksumLine.Count -gt 1) {
        throw "Multiple checksum entries found for $Archive - possible tampering"
    }
    $Expected = ($ChecksumLine -split '\s+')[0].ToLower()
    $Actual = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLower()
    if ($Expected -ne $Actual) {
        throw "Checksum mismatch for ${Archive}: expected $Expected, got $Actual"
    }

    Write-Host "=> Extracting..."
    Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force

    # Create install directories
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    New-Item -ItemType Directory -Path "$FgmDir\versions" -Force | Out-Null
    New-Item -ItemType Directory -Path "$FgmDir\bin" -Force | Out-Null

    # Move binary
    $Src = Join-Path $TmpDir "fgm.exe"
    $Dest = Join-Path $InstallDir "fgm.exe"
    Move-Item -Path $Src -Destination $Dest -Force

    # ── PATH setup ───────────────────────────────────────────────────
    $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    $PathsToAdd = @($InstallDir, "$env:USERPROFILE\go\bin")
    $Changed = $false

    foreach ($p in $PathsToAdd) {
        if ($UserPath -notlike "*$p*") {
            $UserPath = "$p;$UserPath"
            $Changed = $true
        }
    }

    if ($Changed) {
        [Environment]::SetEnvironmentVariable("PATH", $UserPath, "User")
        Write-Host "=> Updated user PATH" -ForegroundColor Blue
    }

    # Also update current session
    $env:PATH = "$InstallDir;$env:USERPROFILE\go\bin;$env:PATH"

    Write-Host ""
    Write-Host "  FGM $Version installed to $Dest" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Please restart your terminal, then run:"
    Write-Host ""
    Write-Host "    fgm install latest" -ForegroundColor Yellow
    Write-Host ""
}
finally {
    Remove-Item -Path $TmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
