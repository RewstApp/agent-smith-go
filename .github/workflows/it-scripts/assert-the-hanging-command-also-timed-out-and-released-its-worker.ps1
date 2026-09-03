#!/usr/bin/env pwsh
#Requires -Version 7

# The two bounds compose: the same command is reported as truncated
# (asserted above) and as timed out, neither masking the other.
$baseline = [int]$env:TIMED_OUT_BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command timed out"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Verbose hanging command was killed by the timeout (count $count > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Verbose hanging command was not killed by the per-command timeout (count stayed at $baseline)"
exit 1

