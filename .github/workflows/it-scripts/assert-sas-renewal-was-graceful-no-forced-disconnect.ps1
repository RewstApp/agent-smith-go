#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:LOST_BASELINE
$content = Get-Content $env:LOG_FILE -Raw
$lost = ([regex]::Matches($content, [regex]::Escape("Connection lost"))).Count
if ($lost -gt $baseline) {
  Write-Error "SAS renewal was not graceful: 'Connection lost' count rose from $baseline to $lost (token expired before renewal fired?)"
  exit 1
}
Write-Output "Confirmed graceful renewal: no new 'Connection lost' (count stayed at $baseline)"

