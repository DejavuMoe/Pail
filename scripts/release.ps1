#requires -Version 7.3
<#
.SYNOPSIS
  Builds Pail (pail-desktop GUI and pail CLI) release archives into <OutDir>/<Version>/.
.DESCRIPTION
  mygo is pure Go (purego, no cgo) on windows/linux/darwin, so every target is
  cross-compiled from this host with CGO_ENABLED=0. Any failed target fails
  the script. Binaries are not developer-signed or notarized.
  Both binaries receive -X main.version. Runs under pwsh 7.3+ on all hosts.
.PARAMETER OutDir
  Parent directory of the per-version output folder (default <repo>/build/release).
  <OutDir>/<Version>/ is deleted and recreated on every run.
#>
[CmdletBinding()]
param(
    [ValidatePattern('^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$')]
    [string]$Version = '0.0.0-dev',
    [switch]$CliOnly,   # skip the GUI binary (no macOS .app is produced)
    [string[]]$Targets = @('windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'),
    [string]$OutDir = ''
)
$ErrorActionPreference = 'Stop'
# `pwsh -File release.ps1 -Targets a,b` (bash, CI YAML) passes one string "a,b"; accept both forms.
$Targets = @($Targets | ForEach-Object { $_ -split ',' } | ForEach-Object Trim | Where-Object { $_ })
if (-not $Targets.Count -or @($Targets | Select-Object -Unique).Count -ne $Targets.Count) { throw 'Targets must be nonempty and unique' }
foreach ($t in $Targets) { if ($t -notmatch '^(windows|linux|darwin)/(amd64|arm64)$') { throw "Unsupported target '$t'" } }
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
    foreach ($d in $out, $stage) {
        $resolved = [IO.Path]::GetFullPath($d)
        $parent = [IO.Path]::GetFullPath((Split-Path -Parent $d)).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
        if (-not $resolved.StartsWith($parent, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path -Leaf $resolved) -ne $Version) { throw "Unsafe output path: $resolved" }
        if (Test-Path -LiteralPath $resolved) {
            if ((Get-Item -LiteralPath $resolved).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Refusing linked output: $resolved" }
            Remove-Item -LiteralPath $resolved -Recurse -Force
        }
        New-Item -ItemType Directory -Force $resolved | Out-Null
    }

    $gen = Join-Path $root 'assets/brand/generated'
    foreach ($f in 'pail.icns', 'pail-256.png') {
        if (-not (Test-Path (Join-Path $gen $f))) { throw "missing $(Join-Path $gen $f); run: go run ./tools/genicons" }
    }

    $apps = @(
        @{ Name = 'pail-desktop'; Pkg = './cmd/pail-desktop'; Gui = $true },
        @{ Name = 'pail'; Pkg = './cmd/pail'; Gui = $false }
    )
    if ($CliOnly) { $apps = @($apps | Where-Object { -not $_.Gui }) }

    # Archives are written by tools/pack so executables keep mode 0755 even when
    # the host has no Unix permissions (Windows).
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    $packer = Join-Path $stage ('pack' + ($IsWindows ? '.exe' : ''))
    & go build -o $packer ./tools/pack
    if ($LASTEXITCODE -ne 0) { throw 'building tools/pack failed' }
    function New-Archive([string]$dir, [string]$archive, [string]$prefix) {
        & $packer -C $dir -o $archive -prefix $prefix -exec "$desktopName,$cliName"
        if ($LASTEXITCODE -ne 0) { throw "packing $archive failed" }
    }

    $archives = [System.Collections.Generic.List[string]]::new()
    $env:CGO_ENABLED = '0'

    foreach ($t in $Targets) {
        $os, $arch = $t -split '/'
        $dir = Join-Path $stage "$os-$arch"
        New-Item -ItemType Directory -Force $dir | Out-Null
        $env:GOOS = $os; $env:GOARCH = $arch
        $suffix = "v$Version-$os-$arch"
        $extension = if ($os -eq 'windows') { '.exe' } else { '' }
        $desktopName = "pail-desktop-$suffix$extension"
        $cliName = "pail-$suffix$extension"
        foreach ($a in $apps) {
            $exe = "$($a.Name)-$suffix$extension"
            $ld = "-s -w -X main.version=$Version"
            if ($a.Gui -and $os -eq 'windows') { $ld += ' -H windowsgui' }
            & go build -trimpath -ldflags $ld -o (Join-Path $dir $exe) $a.Pkg 2>&1 | ForEach-Object { Write-Host "  $_" }
            if ($LASTEXITCODE -ne 0) {
                throw "$t : build of $($a.Name) failed"
            }
        }
        Remove-Item Env:GOOS, Env:GOARCH
        $base = "Pail-$suffix"
        Copy-Item -LiteralPath (Join-Path $root 'LICENSE') -Destination $dir -ErrorAction Stop
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
Exec=$desktopName
Icon=pail
Categories=Network;Utility;
Terminal=false
"@ | Set-Content (Join-Path $dir 'pail.desktop') -Encoding utf8NoBOM
                Copy-Item (Join-Path $gen 'pail-256.png') (Join-Path $dir 'pail.png')
                $archive = Join-Path $out "$base.tar.gz"
                New-Archive $dir $archive $base
            }
            'darwin' {
                if ($CliOnly) { $archive = Join-Path $out "$base.zip"; New-Archive $dir $archive $base; break }
                $app = Join-Path $dir 'Pail.app/Contents'
                New-Item -ItemType Directory -Force (Join-Path $app 'MacOS'), (Join-Path $app 'Resources') | Out-Null
                Move-Item -LiteralPath (Join-Path $dir $desktopName) -Destination (Join-Path $app "MacOS/$desktopName")
                Copy-Item (Join-Path $gen 'pail.icns') (Join-Path $app 'Resources/pail.icns')
                $plistVer = $Version -replace '[-+].*$', ''
                @"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Pail</string>
  <key>CFBundleDisplayName</key><string>Pail</string>
  <key>CFBundleIdentifier</key><string>moe.dejavu.pail</string>
  <key>CFBundleExecutable</key><string>$desktopName</string>
  <key>CFBundleIconFile</key><string>pail</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>$plistVer</string>
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

    $sums = foreach ($f in $archives) { '{0}  {1}' -f (Get-FileHash $f -Algorithm SHA256).Hash.ToLower(), (Split-Path -Leaf $f) }
    if ($sums) { $sums | Set-Content (Join-Path $out 'SHA256SUMS') -Encoding utf8NoBOM }
    Remove-Item -LiteralPath $stage -Recurse -Force
    Write-Host "done: $($archives.Count) archive(s)"
    Write-Host "output directory: $out"
} finally {
    foreach ($k in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($k, $savedEnv[$k]) }
    Pop-Location
}
