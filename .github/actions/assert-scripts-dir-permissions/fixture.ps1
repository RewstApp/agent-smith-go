#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Asserts the command scripts directory has the permissions EnsureSecureDir
enforces (sc-108848).

.DESCRIPTION
The directory is created on demand the first time the agent executes a
command, so this must run after at least one command has been sent.

On Linux and macOS, EnsureSecureDir moved the directory off shared system temp
into the agent-owned data directory and re-asserts mode 0700 and ownership by
the service account on every command, not only when the directory is first
created. `stat` only needs search/execute permission on the ancestor
directories to read the target's own metadata (unlike listing its contents),
so this runs unprivileged even though the directory itself denies access to
every other account.

On Windows, the directory deliberately was NOT moved: some customers have
their endpoint security software configured to whitelist the historical
C:\RewstRemoteAgent\scripts\<orgId> path specifically so the agent's
dynamically-written PowerShell scripts are not flagged or blocked as they run,
and relocating it would silently break command execution on those endpoints.
There are no POSIX mode/ownership bits to assert there, so this only confirms
the directory exists at that unchanged location - the regression this guards
against is someone in the future "fixing" Windows to match Linux/macOS and
breaking that whitelisting.
#>

param(
    [Parameter(Mandatory)][string]$ScriptsDir,
    [string]$ExpectedOwner = ''
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $ScriptsDir)) {
    Write-Error "Scripts directory does not exist: $ScriptsDir (has a command been sent yet?)"
    exit 1
}

if ($IsWindows) {
    Write-Output "Confirmed the scripts directory exists at its historical, unchanged location: $ScriptsDir"
    Write-Output "(Windows intentionally keeps this path so existing AV/EDR whitelisting is not broken; see EnsureSecureDir's Windows behavior.)"
    exit 0
}

if ($IsMacOS) {
    $raw = & stat -f '%Lp %Su' -- $ScriptsDir
} else {
    $raw = & stat -c '%a %U' -- $ScriptsDir
}

$parts = $raw.Trim() -split '\s+'
if ($parts.Count -lt 2) {
    Write-Error "Could not parse stat output for ${ScriptsDir}: '$raw'"
    exit 1
}
$mode = $parts[0]
$owner = $parts[1]

Write-Output "Scripts directory $ScriptsDir -> mode=$mode owner=$owner (expected mode=700, owner=$ExpectedOwner)"

if ($mode -ne '700') {
    Write-Error "Expected mode 700 on $ScriptsDir, got $mode - EnsureSecureDir should have corrected this on the last command"
    exit 1
}

if ($ExpectedOwner -and $owner -ne $ExpectedOwner) {
    Write-Error "Expected owner '$ExpectedOwner' on $ScriptsDir, got '$owner'"
    exit 1
}

Write-Output "Confirmed hardened permissions (mode 700, owner $owner) on the command scripts directory"
exit 0
