#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 30; $i++) {
  Start-Sleep -Seconds 2
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Worker recovered after the verbose timeout (count $count > baseline $baseline)"
    exit 0
  }
}
Write-Error "No command completed after the verbose timeout (count stayed at $baseline)"
exit 1

