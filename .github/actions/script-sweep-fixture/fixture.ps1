#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Seeds (mode seed) or verifies (mode assert) the file fixture for the startup
stale script sweep scenario (sc-103967).

.DESCRIPTION
Three control files are placed in the agent's scripts directory:

  exec-900000001.ps1  matches the agent's own temp script pattern and is aged
                      past the sweep threshold, so it must be reclaimed
  exec-900000002.ps1  matches the pattern but keeps the current timestamp,
                      standing in for a command that is still executing
  operator-script.ps1 is aged but is not a name the agent ever creates, so the
                      sweep must leave it alone however stale it is

When -OrphanPath is supplied it is the script file a force-killed command left
behind. Seeding requires that file to still exist - it is the leak the sweep
exists to clean up, so its absence means the scenario is not exercising
anything - and ages it alongside the control files. Ageing is the only thing
simulated here: it is exactly what a long-lived install does by staying up.
#>

param(
    [Parameter(Mandatory)][ValidateSet('seed', 'assert')][string]$Mode,
    [Parameter(Mandatory)][string]$ScriptsDir,
    [string]$OrphanPath = '',
    [int]$StaleAgeHours = 48
)

$ErrorActionPreference = 'Stop'

$stale = Join-Path $ScriptsDir 'exec-900000001.ps1'
$fresh = Join-Path $ScriptsDir 'exec-900000002.ps1'
$foreign = Join-Path $ScriptsDir 'operator-script.ps1'

function Show-ScriptsDir {
    Write-Output "Contents of ${ScriptsDir}:"
    Get-ChildItem -LiteralPath $ScriptsDir -Force |
        Select-Object Name, Length, LastWriteTime |
        Format-Table |
        Out-String |
        Write-Output
}

if (-not (Test-Path -LiteralPath $ScriptsDir)) {
    Write-Error "Scripts directory does not exist: $ScriptsDir"
    exit 1
}

if ($Mode -eq 'seed') {
    if ($OrphanPath) {
        if (-not (Test-Path -LiteralPath $OrphanPath)) {
            Write-Error "Expected the force-killed command to leave $OrphanPath behind, but it is already gone"
            exit 1
        }
        Write-Output "Confirmed the interrupted command leaked $OrphanPath"
    }

    Set-Content -LiteralPath $stale -Value '# stale agent script (integration test)'
    Set-Content -LiteralPath $fresh -Value '# fresh agent script (integration test)'
    Set-Content -LiteralPath $foreign -Value '# operator script (integration test)'

    $aged = (Get-Date).AddHours(-$StaleAgeHours)
    $toAge = @($stale, $foreign)
    if ($OrphanPath) { $toAge += $OrphanPath }
    foreach ($path in $toAge) {
        (Get-Item -LiteralPath $path -Force).LastWriteTime = $aged
        Write-Output "Aged $path to $aged"
    }

    Show-ScriptsDir
    exit 0
}

$failed = $false

$mustBeGone = @($stale)
if ($OrphanPath) { $mustBeGone += $OrphanPath }
foreach ($path in $mustBeGone) {
    if (Test-Path -LiteralPath $path) {
        Write-Output "FAIL: stale script file was not swept: $path"
        $failed = $true
    }
    else {
        Write-Output "OK: stale script file was swept: $path"
    }
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

Show-ScriptsDir

if ($failed) {
    Write-Error 'Startup script sweep did not behave as expected'
    exit 1
}

Write-Output 'Startup script sweep reclaimed the stale agent script files and nothing else'
