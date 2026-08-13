#!/usr/bin/env pwsh
#Requires -Version 7

# Writes (or removes) the release-url override file the integration build reads
# to decide which endpoint the auto-updater queries (sc-106110; see
# agent.ResolveLatestReleaseUrl).
#
# The override lives beside the agent's config in the org's data directory
# because that directory is what the service account can already read on all
# three platforms, whatever account the service runs as. The file is read when a
# service cycle constructs its updater, so writing it takes effect on the next
# service start rather than mid-run.
#
# The directory belongs to the service account (root / SYSTEM), so this runs
# elevated; the wrapping action supplies the elevation per platform.

param(
    [Parameter(Mandatory = $true)][string]$OverrideFile,
    [Parameter(Mandatory = $true)][ValidateSet('set', 'clear')][string]$Mode,
    [string]$Url
)

$ErrorActionPreference = 'Stop'

if ($Mode -eq 'clear') {
    # Tolerates a missing file so an always() cleanup can run even when the
    # scenario failed before writing one.
    Remove-Item -Path $OverrideFile -Force -ErrorAction SilentlyContinue
    Write-Output "release url override cleared ($OverrideFile)"
    exit 0
}

if ([string]::IsNullOrWhiteSpace($Url)) {
    Write-Error "a url is required when setting the release url override"
    exit 1
}

$directory = Split-Path -Parent $OverrideFile
if (-not (Test-Path $directory)) {
    Write-Error "agent data directory not found at $directory"
    exit 1
}

Set-Content -Path $OverrideFile -Value $Url -NoNewline
# The service may run as an account other than the one that wrote this, so the
# file has to stay readable by it; the data directory's own permissions decide
# who can get this far.
if ($IsLinux -or $IsMacOS) {
    chmod 0644 $OverrideFile
}
Write-Output "release url override set to $Url ($OverrideFile)"
