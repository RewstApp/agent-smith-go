#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $swept = if ($content) { ([regex]::Matches($content, [regex]::Escape("Swept stale script files"))).Count } else { 0 }
  if ($swept -gt $baseline) {
    Write-Output "Startup sweep logged (count $swept > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Startup sweep was never logged (count stayed at $baseline)"
exit 1

