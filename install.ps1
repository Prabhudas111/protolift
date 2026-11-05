# Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
# SPDX-License-Identifier: Apache-2.0
#
# Installs the latest protolift release for Windows.
#   irm https://raw.githubusercontent.com/Prabhudas111/protolift/main/install.ps1 | iex
# Set $env:PROTOLIFT_VERSION = "v0.1.0" to pick a version.
$ErrorActionPreference = "Stop"
$repo = "Prabhudas111/protolift"

# GitHub sometimes answers a download with a short-lived 5xx. Each request
# is tried up to 5 times with a pause. The checksum still decides.
function Get-WithRetry([string]$Uri, [string]$OutFile, [int]$TimeoutSec) {
  for ($i = 1; $i -le 5; $i++) {
    try {
      if ($OutFile) { Invoke-WebRequest -UseBasicParsing -TimeoutSec $TimeoutSec -Uri $Uri -OutFile $OutFile; return }
      return Invoke-RestMethod -TimeoutSec $TimeoutSec -Uri $Uri
    } catch {
      if ($i -eq 5) { throw "protolift install: download failed after 5 tries: $Uri ($($_.Exception.Message))" }
      Start-Sleep -Seconds (3 * $i)
    }
  }
}

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$version = $env:PROTOLIFT_VERSION
if (-not $version) {
  $version = (Get-WithRetry -Uri "https://api.github.com/repos/$repo/releases/latest" -TimeoutSec 30).tag_name
}
if (-not $version) { throw "protolift install: could not find the latest release" }

$file = "protolift_$($version.TrimStart('v'))_windows_$arch.zip"
$base = "https://github.com/$repo/releases/download/$version"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("protolift-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading protolift $version for windows/$arch..."
  Get-WithRetry -Uri "$base/$file" -OutFile (Join-Path $tmp $file) -TimeoutSec 300
  Get-WithRetry -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt") -TimeoutSec 60

  $line = Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match " $([regex]::Escape($file))$" }
  if (-not $line) { throw "protolift install: no checksum listed for $file" }
  $want = ($line -split " ")[0]
  $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $file)).Hash.ToLower()
  if ($want -ne $got) { throw "protolift install: checksum mismatch, the download was not used" }

  Expand-Archive -Path (Join-Path $tmp $file) -DestinationPath $tmp -Force
  $dest = Join-Path $env:LOCALAPPDATA "Programs\protolift"
  New-Item -ItemType Directory -Force -Path $dest | Out-Null
  Copy-Item (Join-Path $tmp "protolift.exe") (Join-Path $dest "protolift.exe") -Force

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (($userPath -split ";") -notcontains $dest) {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$dest", "User")
    Write-Host "Added $dest to your PATH (open a new terminal)."
  }
  Write-Host "Installed $dest\protolift.exe"
  Write-Host "Next: protolift init   ·   Built by Proto.Engineer: https://www.proto-engineer.com"
} finally {
  Remove-Item -Recurse -Force $tmp
}
