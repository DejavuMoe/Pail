#requires -Version 7.3
# Optional compatibility wrapper. The Go tool owns all build and packaging logic.
[CmdletBinding()]
param(
    [string]$Version = '0.0.0-dev',
    [string[]]$Targets = @(),
    [string]$OutDir = '',
    [switch]$CliOnly
)
$ErrorActionPreference = 'Stop'
$arguments = @('-version', $Version)
if ($Targets.Count) { $arguments += @('-targets', ($Targets -join ',')) }
if ($OutDir) { $arguments += @('-out-dir', $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutDir)) }
if ($CliOnly) { $arguments += '-cli-only' }
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    & go run ./tools/release @arguments
    if ($LASTEXITCODE -ne 0) { throw "release failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
