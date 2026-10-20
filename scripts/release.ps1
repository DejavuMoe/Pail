#requires -Version 7.3
<#
.SYNOPSIS
  Builds Pail (pail-desktop GUI and pail CLI) release archives into <OutDir>/<Version>/.
.DESCRIPTION
  mygo is pure Go (purego, no cgo) on windows/linux/darwin, so every target is
  cross-compiled from this host with CGO_ENABLED=0. A target that fails to build
  is skipped with a WARN line and listed in NOT-BUILT.txt; no placeholder
  artifacts are ever written. Binaries are not code-signed or notarized.
  -X main.version is added only for binaries whose package main declares a
  top-level `version` variable. Runs under pwsh 7.3+ on Windows, Linux and macOS.
.PARAMETER OutDir
  Parent directory of the per-version output folder (default <repo>/build/release).
  <OutDir>/<Version>/ is deleted and recreated on every run.
#>
[CmdletBinding()]
param(
    [ValidatePattern('^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$')]
    [string]$Version = '0.0.0-dev',
    [switch]$CliOnly,   # skip the GUI binary (no macOS .app is produced)
    [string[]]$Targets = @('windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'),
    [string]$OutDir = ''
)
$ErrorActionPreference = 'Stop'
# `pwsh -File release.ps1 -Targets a,b` (bash, CI YAML) passes one string "a,b"; accept both forms.
$Targets = @($Targets | ForEach-Object { $_ -split ',' } | ForEach-Object Trim | Where-Object { $_ })
foreach ($t in $Targets) { if ($t -notmatch '^[a-z0-9]+/[a-z0-9]+$') { throw "Invalid target '$t' (expected os/arch, e.g. linux/amd64)" } }
$root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $root 'go.mod') -PathType Leaf)) { throw "Expected the project root at $root" }
# Resolve a relative -OutDir against the caller's location, before changing directory.
$OutDir = if ($OutDir) { $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutDir) } else { Join-Path $root 'build/release' }
$out = Join-Path $OutDir $Version
$stage = Join-Path $root "build/stage/$Version"

$savedEnv = @{}
foreach ($k in 'CGO_ENABLED', 'GOOS', 'GOARCH') { $savedEnv[$k] = [Environment]::GetEnvironmentVariable($k) }
Push-Location $root
try {
    foreach ($d in $out, $stage) { if (Test-Path $d) { Remove-Item -Recurse -Force $d }; New-Item -ItemType Directory -Force $d | Out-Null }

    $gen = Join-Path $root 'assets/brand/generated'
    foreach ($f in 'pail.icns', 'pail-256.png') {
        if (-not (Test-Path (Join-Path $gen $f))) { throw "missing $(Join-Path $gen $f); run: go run ./tools/genicons" }
    }

    function Test-HasVersionVar([string]$dir) {
        foreach ($f in Get-ChildItem $dir -Filter *.go -File | Where-Object Name -NotLike '*_test.go') {
            if ((Get-Content $f.FullName -Raw) -match '(?m)^var version\b|^\tversion\s*(string\s*)?=\s*"') { return $true }
        }
        return $false
    }
    $apps = @(
        @{ Name = 'pail-desktop'; Pkg = './cmd/pail-desktop'; Gui = $true },
        @{ Name = 'pail'; Pkg = './cmd/pail'; Gui = $false }
    )
    if ($CliOnly) { $apps = @($apps | Where-Object { -not $_.Gui }) }
    foreach ($a in $apps) { $a.HasVer = Test-HasVersionVar (Join-Path $root $a.Pkg) }

    # Archives are written by tools/pack so executables keep mode 0755 even when
    # the host has no Unix permissions (Windows).
    $packer = Join-Path $stage ('pack' + ($IsWindows ? '.exe' : ''))
    & go build -o $packer ./tools/pack
    if ($LASTEXITCODE -ne 0) { throw 'building tools/pack failed' }
    function New-Archive([string]$dir, [string]$archive, [string]$prefix) {
        & $packer -C $dir -o $archive -prefix $prefix -exec 'pail-desktop,pail'
        if ($LASTEXITCODE -ne 0) { throw "packing $archive failed" }
    }

    $notBuilt = [System.Collections.Generic.List[string]]::new()
    $archives = [System.Collections.Generic.List[string]]::new()
    $env:CGO_ENABLED = '0'

    foreach ($t in $Targets) {
        $os, $arch = $t -split '/'
        $dir = Join-Path $stage "$os-$arch"
        New-Item -ItemType Directory -Force $dir | Out-Null
        $env:GOOS = $os; $env:GOARCH = $arch
        $ok = $true
        foreach ($a in $apps) {
            $exe = $a.Name + ($os -eq 'windows' ? '.exe' : '')
            $ld = '-s -w'
            if ($a.HasVer) { $ld += " -X main.version=$Version" }
            if ($a.Gui -and $os -eq 'windows') { $ld += ' -H windowsgui' }
            & go build -trimpath -ldflags $ld -o (Join-Path $dir $exe) $a.Pkg 2>&1 | ForEach-Object { Write-Host "  $_" }
            if ($LASTEXITCODE -ne 0) {
                Write-Warning "$t : build of $($a.Name) failed; skipping target"
                $notBuilt.Add("$t : go build $($a.Pkg) failed (see log above)")
                $ok = $false; break
            }
        }
        Remove-Item Env:GOOS, Env:GOARCH
        if (-not $ok) { continue }

        $base = "Pail-$Version-$os-$arch"
        foreach ($f in 'LICENSE', 'README.md', 'README.zh-CN.md') {
            if (Test-Path (Join-Path $root $f)) { Copy-Item (Join-Path $root $f) $dir } elseif ($f -ne 'README.zh-CN.md') { throw "missing $f" }
        }
        switch ($os) {
            'windows' {
                $archive = Join-Path $out "$base.zip"
                New-Archive $dir $archive $base
            }
            'linux' {
                @"
[Desktop Entry]
Type=Application
Name=Pail
Comment=S3 storage manager
Exec=pail-desktop
Icon=pail
Categories=Network;Utility;
Terminal=false
"@ | Set-Content (Join-Path $dir 'pail.desktop') -Encoding utf8NoBOM
                Copy-Item (Join-Path $gen 'pail-256.png') (Join-Path $dir 'pail.png')
                $archive = Join-Path $out "$base.tar.gz"
                New-Archive $dir $archive $base
            }
            'darwin' {
                $app = Join-Path $dir 'Pail.app/Contents'
                New-Item -ItemType Directory -Force (Join-Path $app 'MacOS'), (Join-Path $app 'Resources') | Out-Null
                if ($CliOnly) { $archive = Join-Path $out "$base.zip"; New-Archive $dir $archive $base; break }
                Move-Item (Join-Path $dir 'pail-desktop') (Join-Path $app 'MacOS/pail-desktop')
                Copy-Item (Join-Path $gen 'pail.icns') (Join-Path $app 'Resources/pail.icns')
                $plistVer = $Version -replace '-.*$', ''
                @"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Pail</string>
  <key>CFBundleDisplayName</key><string>Pail</string>
  <key>CFBundleIdentifier</key><string>moe.dejavu.pail</string>
  <key>CFBundleExecutable</key><string>pail-desktop</string>
  <key>CFBundleIconFile</key><string>pail</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>$Version</string>
  <key>CFBundleShortVersionString</key><string>$plistVer</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
"@ | Set-Content (Join-Path $app 'Info.plist') -Encoding utf8NoBOM
                $archive = Join-Path $out "$base.zip"
                New-Archive $dir $archive $base
            }
        }
        $archives.Add($archive)
        Write-Host "built $archive"
    }

    if ($notBuilt.Count -gt 0) { $notBuilt | Set-Content (Join-Path $out 'NOT-BUILT.txt') -Encoding utf8NoBOM }
    $sums = foreach ($f in $archives) { '{0}  {1}' -f (Get-FileHash $f -Algorithm SHA256).Hash.ToLower(), (Split-Path -Leaf $f) }
    if ($sums) { $sums | Set-Content (Join-Path $out 'SHA256SUMS') -Encoding utf8NoBOM }
    Remove-Item -Recurse -Force $stage
    Write-Host "done: $($archives.Count) archive(s), $($notBuilt.Count) target(s) not built"
    Write-Host "output directory: $out"
} finally {
    foreach ($k in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($k, $savedEnv[$k]) }
    Pop-Location
}
