#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Seeds (mode seed) or verifies (mode assert) the file fixture for the startup
downloaded-installer sweep scenario (sc-106111).

.DESCRIPTION
Three control files are placed in the agent's updates directory:

  installer-900000001.bin  matches the name pattern the updater downloads under
                           and is aged past the sweep threshold, so it must be
                           reclaimed
  installer-900000002.bin  matches the pattern but keeps the current timestamp,
                           standing in for the installer of the update that is
                           still running - the file the sweep must never take
  vendor-installer.bin     is aged but is not a name the agent ever creates, so
                           the sweep must leave it alone however stale it is

The files are sized in megabytes rather than bytes, so the reclaimed space is
visible in the directory listing the fixture prints: the leak this scenario
covers is measured in tens of megabytes per update, not in file counts.

The updates directory is created if it does not exist. An installation that has
never completed an update has never created it, which is the normal state on a
CI runner - the scenario exercises the sweep, not the download.

Ageing is the only thing simulated here: it is exactly what a long-lived install
does by staying up. The production 24h threshold stays in play, so no test-only
override is needed in the agent.

Symlink handling is asserted by the unit tests rather than here. Aging a symlink
without following it needs a different call on every platform, and an un-aged
link would survive on the age check alone - making the assertion pass without
proving the sweep declined to follow it.
#>

param(
    [Parameter(Mandatory)][ValidateSet('seed', 'assert')][string]$Mode,
    [Parameter(Mandatory)][string]$UpdatesDir,
    [int]$StaleAgeHours = 48
)

$ErrorActionPreference = 'Stop'

$stale = Join-Path $UpdatesDir 'installer-900000001.bin'
$fresh = Join-Path $UpdatesDir 'installer-900000002.bin'
$foreign = Join-Path $UpdatesDir 'vendor-installer.bin'

function Show-UpdatesDir {
    Write-Output "Contents of ${UpdatesDir}:"
    Get-ChildItem -LiteralPath $UpdatesDir -Force |
        Select-Object Name, Length, LastWriteTime |
        Format-Table |
        Out-String |
        Write-Output
}

if ($Mode -eq 'seed') {
    if (-not (Test-Path -LiteralPath $UpdatesDir)) {
        New-Item -ItemType Directory -Path $UpdatesDir -Force | Out-Null
        Write-Output "Created updates directory $UpdatesDir"
    }

    # 4 MiB each: small enough to write quickly, large enough that the listing
    # shows the sweep reclaiming real space rather than empty files.
    $payload = [byte[]]::new(4MB)
    foreach ($path in @($stale, $fresh, $foreign)) {
        [System.IO.File]::WriteAllBytes($path, $payload)
    }

    $aged = (Get-Date).AddHours(-$StaleAgeHours)
    foreach ($path in @($stale, $foreign)) {
        (Get-Item -LiteralPath $path -Force).LastWriteTime = $aged
        Write-Output "Aged $path to $aged"
    }

    Show-UpdatesDir
    exit 0
}

if (-not (Test-Path -LiteralPath $UpdatesDir)) {
    Write-Error "Updates directory does not exist: $UpdatesDir"
    exit 1
}

$failed = $false

if (Test-Path -LiteralPath $stale) {
    Write-Output "FAIL: stale installer file was not swept: $stale"
    $failed = $true
}
else {
    Write-Output "OK: stale installer file was swept: $stale"
}

foreach ($path in @($fresh, $foreign)) {
    if (Test-Path -LiteralPath $path) {
        Write-Output "OK: file the sweep must not touch survived: $path"
    }
    else {
        Write-Output "FAIL: sweep removed a file it must not touch: $path"
        $failed = $true
    }
}

Show-UpdatesDir

if ($failed) {
    Write-Error 'Startup installer sweep did not behave as expected'
    exit 1
}

Write-Output 'Startup installer sweep reclaimed the stale downloaded installer and nothing else'
