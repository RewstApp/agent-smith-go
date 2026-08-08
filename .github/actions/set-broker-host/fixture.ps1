#!/usr/bin/env pwsh
#Requires -Version 7

# Rewrites azure_iot_hub_host in the agent's config file, and records the value
# it replaced so the caller can put the original back.
#
# The agent exposes no flag for the broker host - it is provisioned once from the
# Rewst configuration endpoint - so pointing a test agent at the stub broker
# (sc-106107) means editing the config file directly. The agent only reads the
# config when a service cycle starts, so the edit takes effect on the next
# service restart rather than mid-run.
#
# The config file belongs to the service account (root / SYSTEM), so this runs
# elevated; the wrapping action supplies the elevation per platform. The previous
# host is written to a file rather than to $GITHUB_OUTPUT because sudo does not
# carry the runner's environment through by default.

param(
    [Parameter(Mandatory = $true)][string]$ConfigFile,
    [Parameter(Mandatory = $true)][string]$BrokerHost,
    [Parameter(Mandatory = $true)][string]$PreviousHostFile
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $ConfigFile)) {
    Write-Error "config file not found at $ConfigFile"
    exit 1
}

$config = Get-Content $ConfigFile -Raw | ConvertFrom-Json
$previous = $config.azure_iot_hub_host

if ([string]::IsNullOrWhiteSpace($previous)) {
    Write-Error "config file has no azure_iot_hub_host to replace"
    exit 1
}

$config.azure_iot_hub_host = $BrokerHost
$config | ConvertTo-Json -Depth 10 | Set-Content $ConfigFile

Set-Content -Path $PreviousHostFile -Value $previous -NoNewline
Write-Output "azure_iot_hub_host: $previous -> $BrokerHost"
