#Requires -Version 7.3
<#
.SYNOPSIS
    Runs the external-endpoint (TestExt*) suite against a real S3-compatible server, or removes a stray run prefix.

.DESCRIPTION
    Credentials come ONLY from environment variables that must already be set; their values are never printed or written:
      PAIL_EXT_ENDPOINT, PAIL_EXT_ACCESS_KEY, PAIL_EXT_SECRET_KEY   (required)
      PAIL_EXT_BUCKET (default test), PAIL_EXT_REGION (default us-east-1), PAIL_EXT_PATH_STYLE (default true), PAIL_EXT_CDN
    The suite writes only below one unique prefix "pail-e2e/<UTC>-<random>/" and removes it afterwards.
    After the run the log is checked for the secret key and the access key; any hit fails the script.

.PARAMETER LogPath
    Where the complete go test -v output is written (default build/ext-e2e.log).

.PARAMETER Run
    go test -run expression (default '^TestExt').

.PARAMETER CleanupOnly
    Do not run the suite; remove every object (all versions) and multipart upload below -Prefix.

.PARAMETER Prefix
    With -CleanupOnly: the stray prefix, e.g. pail-e2e/20261019-123946-c53a00/. Must start with pail-e2e/ and name a run below it.
#>
[CmdletBinding()]
param(
    [string]$LogPath = '',
    [string]$Run = '^TestExt',
    [switch]$CleanupOnly,
    [string]$Prefix = ''
)

$ErrorActionPreference = 'Stop'
$project = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $project 'go.mod') -PathType Leaf)) {
    throw "Expected the project root at $project"
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'go must be on PATH.' }

$required = 'PAIL_EXT_ENDPOINT', 'PAIL_EXT_ACCESS_KEY', 'PAIL_EXT_SECRET_KEY'
$missing = @($required | Where-Object { [string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($_)) })
if ($missing.Count -gt 0) {
    throw ('Missing environment variable(s): ' + ($missing -join ', ') + '. Set them in this session first; values are never echoed.')
}
if ($Run -notmatch '^[A-Za-z0-9_|^$().*-]+$') { throw 'Invalid -Run expression.' }

$secret = $env:PAIL_EXT_SECRET_KEY
$access = $env:PAIL_EXT_ACCESS_KEY

if ($CleanupOnly) {
    if ($Prefix -notmatch '^pail-e2e/[A-Za-z0-9_./-]+$' -or $Prefix.Contains('..') -or $Prefix.StartsWith('pail-e2e//') -or $Prefix.Trim('/') -eq 'pail-e2e') {
        throw "Refusing prefix '$Prefix': it must start with 'pail-e2e/' and name a run below it (for example pail-e2e/20261019-123946-c53a00/)."
    }
    if (-not $Prefix.EndsWith('/')) { $Prefix += '/' }
    Push-Location $project
    try {
        $env:PAIL_EXT_CLEANUP_PREFIX = $Prefix
        & go test ./integration -run '^TestExtCleanupOnly$' -count=1 -v -timeout 30m
        if ($LASTEXITCODE -ne 0) { throw "Cleanup of $Prefix failed (exit code $LASTEXITCODE)." }
    } finally {
        Remove-Item Env:PAIL_EXT_CLEANUP_PREFIX -ErrorAction SilentlyContinue
        Pop-Location
    }
    return
}

if (-not $LogPath) { $LogPath = Join-Path $project 'build/ext-e2e.log' }
$LogPath = [IO.Path]::GetFullPath($LogPath, $project)
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $LogPath) | Out-Null
Remove-Item Env:PAIL_EXT_CLEANUP_PREFIX -ErrorAction SilentlyContinue

$started = Get-Date
Push-Location $project
try {
    & go test ./integration -run $Run -count=1 -v -timeout 30m *>&1 | Tee-Object -FilePath $LogPath
    $code = $LASTEXITCODE
} finally {
    Pop-Location
}
Add-Content -LiteralPath $LogPath -Value ("`n[test-external] go test exit code $code, started $($started.ToUniversalTime().ToString('o')), duration $([int]((Get-Date) - $started).TotalSeconds)s")

# Secret scan: the credentials must never have been printed in the first place; verify that.
$text = [IO.File]::ReadAllText($LogPath)
$leaks = [System.Collections.Generic.List[string]]::new()
foreach ($pair in @(@('secret key', $secret), @('access key', $access))) {
    $variants = @($pair[1], [Uri]::EscapeDataString($pair[1]), $pair[1].Replace('+', ' ')) | Select-Object -Unique
    foreach ($v in $variants) {
        if ($v.Length -ge 8 -and $text.Contains($v, [StringComparison]::Ordinal)) { $leaks.Add($pair[0]); break }
    }
}
if ($leaks.Count -gt 0) {
    throw ("SECURITY: the log $LogPath contains the " + ($leaks -join ' and ') + '. Delete the log and rotate the credentials.')
}
Write-Host "[test-external] secret scan: no occurrence of the secret key or the access key in $LogPath"
if ($code -ne 0) { throw "External S3 suite failed with exit code $code (log: $LogPath)." }
