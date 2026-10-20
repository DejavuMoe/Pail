#Requires -Version 7.3
<#
.SYNOPSIS
    Runs the MinIO integration suite (scripts/integration-minio.sh) against an isolated, throwaway MinIO container.
.DESCRIPTION
    On Linux the shell script runs directly with bash (needs docker, curl and go on PATH).
    On Windows it is dispatched through linux-task.ps1 -Mode build when that helper is on PATH;
    one build-mode invocation owns synchronization and the complete container lifetime, so do not
    run it alongside another build-mode job for this project. Other hosts are not supported.
    Parameters are passed to the shell script as PAIL_IT_* environment variables.
#>
[CmdletBinding()]
param(
    [ValidateRange(1024, 65535)]
    [int]$Port = 19000,

    # 'pinned' = locally built reproducible image (default); 'latest' = newest upstream release tag built from source (-MinioLatestTag);
    # anything else is used as a full image reference (pulled by Docker).
    [string]$MinioImage = 'pinned',

    # Release tag built for -MinioImage latest (newest minio/minio release on GitHub as of 2026-10-19).
    [ValidatePattern('^RELEASE\.[0-9T-]+Z$')]
    [string]$MinioLatestTag = 'RELEASE.2025-10-15T17-29-55Z',

    # Optional: tee the complete go test -v output (and a final exit-code line) to this file.
    [string]$LogPath = '',

    # Optional extra arguments for go test, e.g. '-run TestApp'.
    [string]$GoTestArgs = ''
)

$ErrorActionPreference = 'Stop'
$project = Split-Path -Parent $PSScriptRoot
$script = Join-Path $project 'scripts/integration-minio.sh'
if (-not (Test-Path -LiteralPath (Join-Path $project 'go.mod') -PathType Leaf)) {
    throw "Expected the project root at $project"
}
if (-not (Test-Path -LiteralPath $script -PathType Leaf)) {
    throw "Missing $script"
}
# The shell script validates these again; keep both in sync.
if ($MinioImage -notmatch '^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$') { throw "Invalid -MinioImage: $MinioImage" }
if ($GoTestArgs -notmatch '^[A-Za-z0-9_ .|^$()=/-]*$') { throw "Invalid -GoTestArgs: $GoTestArgs" }

$vars = [ordered]@{
    PAIL_IT_PORT        = [string]$Port
    PAIL_IT_MINIO_IMAGE = $MinioImage
    PAIL_IT_MINIO_TAG   = $MinioLatestTag
    PAIL_IT_GOTEST_ARGS = $GoTestArgs
}

if ($IsLinux) {
    if (-not (Get-Command bash -ErrorAction SilentlyContinue)) { throw 'bash must be on PATH.' }
    $saved = @{}
    foreach ($k in $vars.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $vars[$k]) }
    $invoke = { & bash $script }
} elseif ($IsWindows) {
    if (-not (Get-Command linux-task.ps1 -ErrorAction SilentlyContinue)) {
        throw 'On Windows this script needs linux-task.ps1 (a local WSL build wrapper) on PATH. Without it, run scripts/integration-minio.sh on a Linux host with Docker, or run this script under pwsh on Linux.'
    }
    # Every value matches a whitelist without single quotes, so single-quoting is safe in bash.
    $exports = ($vars.Keys | ForEach-Object { "export $_='$($vars[$_])'" }) -join '; '
    $linuxCommand = "$exports; bash scripts/integration-minio.sh"
    $invoke = { & linux-task.ps1 -Mode build -Project $project -Command $linuxCommand }
} else {
    throw 'The MinIO integration suite runs on Linux (directly) or on Windows through linux-task.ps1; this host is neither.'
}

$started = Get-Date
try {
    if ($LogPath) {
        $LogPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($LogPath)
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $LogPath) | Out-Null
        & $invoke *>&1 | Tee-Object -FilePath $LogPath
    } else {
        & $invoke
    }
    $code = $LASTEXITCODE
} finally {
    if ($IsLinux) {
        foreach ($k in $vars.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
}
if ($LogPath) {
    Add-Content -LiteralPath $LogPath -Value ("`n[test-integration] exit code $code, image '$MinioImage', started $($started.ToString('o')), duration $([int]((Get-Date) - $started).TotalSeconds)s")
}
if ($code -ne 0) {
    throw "Isolated S3 integration run failed with exit code $code."
}
