#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 30; $i++) {
  Start-Sleep -Seconds 2
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command timed out"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Command timed out as expected (count $count > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
}
Write-Error "Hanging command was not killed by the per-command timeout (count stayed at $baseline)"
exit 1

