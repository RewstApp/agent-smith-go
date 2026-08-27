#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Assert config.json contains the required fields and rewst_org_id matches the
expected value, and expose device_id/azure_iot_hub_host as step outputs.

.DESCRIPTION
Reading config.json requires elevation now that it is written owner-only
(sc-108849): the shared implementation runs under sudo on Unix and natively on
the already-elevated Windows runner (see action.yml).
#>

param(
    [Parameter(Mandatory)][string]$ConfigDir,
    [Parameter(Mandatory)][string]$OrgId
)

$ErrorActionPreference = 'Stop'

$configPath = Join-Path $ConfigDir (Join-Path $OrgId "config.json")
if (-not (Test-Path $configPath)) {
    Write-Error "config.json not found at $configPath"
    exit 1
}
$config = Get-Content $configPath -Raw | ConvertFrom-Json

$required = @("device_id", "rewst_org_id", "rewst_engine_host", "shared_access_key", "azure_iot_hub_host")
$failed = $false
foreach ($field in $required) {
    $value = $config.$field
    if ([string]::IsNullOrWhiteSpace($value)) {
        Write-Error "Config field '$field' is missing or empty"
        $failed = $true
    } else {
        Write-Output "OK: $field = $value"
    }
}

if ($config.rewst_org_id -ne $OrgId) {
    Write-Error "rewst_org_id '$($config.rewst_org_id)' does not match expected '$OrgId'"
    $failed = $true
}

if ($failed) { exit 1 }

"device_id=$($config.device_id)" >> $env:GITHUB_OUTPUT
"azure_iot_hub_host=$($config.azure_iot_hub_host)" >> $env:GITHUB_OUTPUT
Write-Output "All config.json fields validated"
