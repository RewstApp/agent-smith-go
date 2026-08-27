#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Assert the service password was not persisted to config.json on disk.

.DESCRIPTION
Reading config.json requires elevation now that it is written owner-only
(sc-108849): the shared implementation runs under sudo on Unix and natively on
the already-elevated Windows runner (see action.yml).

The secret is read from $env:SECRET rather than a parameter, so it never
appears on the command line (and so in the process list a sudo re-exec makes
briefly visible to every local account) - it only ever lives in an
environment variable, same as before this file was split out.
#>

param(
    [Parameter(Mandatory)][string]$ConfigDir,
    [Parameter(Mandatory)][string]$OrgId
)

$ErrorActionPreference = "Stop"
$Secret = $env:SECRET

if ([string]::IsNullOrEmpty($Secret)) {
    Write-Output "No secret supplied; skipping config.json secret check"
    exit 0
}

$configPath = Join-Path $ConfigDir (Join-Path $OrgId "config.json")
if (-not (Test-Path $configPath)) {
    Write-Error "config.json not found at $configPath"
    exit 1
}

$content = Get-Content $configPath -Raw
if ($content.Contains($Secret)) {
    # Avoid echoing the secret itself.
    Write-Output "::error::Service password was persisted to config.json at $configPath"
    exit 1
}

Write-Output "Confirmed service password is absent from config.json"
