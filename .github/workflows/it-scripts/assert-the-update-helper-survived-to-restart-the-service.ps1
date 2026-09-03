#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Service started"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Update helper restarted the service (count $count > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Output "---- last 60 lines of the agent log ----"
Get-Content $env:LOG_FILE -Tail 60 -ErrorAction SilentlyContinue
Write-Error "Update helper never logged 'Service started' after the auto-update; it was likely killed mid-update (count stayed at $baseline)"
exit 1

