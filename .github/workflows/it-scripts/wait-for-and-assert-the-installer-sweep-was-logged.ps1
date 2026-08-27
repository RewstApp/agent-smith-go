#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $swept = if ($content) { ([regex]::Matches($content, [regex]::Escape("Swept stale installer files"))).Count } else { 0 }
  if ($swept -gt $baseline) {
    Write-Output "Installer sweep logged (count $swept > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Output "---- last 40 log lines ----"
Get-Content $env:LOG_FILE -Tail 40 -ErrorAction SilentlyContinue
Write-Error "Installer sweep was never logged (count stayed at $baseline)"
exit 1

