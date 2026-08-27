#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 90; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Retrying update"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Agent is in a retry backoff again after ~${i}s"
    exit 0
  }
  Start-Sleep -Seconds 1
}
Write-Error "The agent never entered a retry backoff after the endpoint was failed again"
exit 1

