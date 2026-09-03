#!/usr/bin/env pwsh
#Requires -Version 7

# Two things at once: the worker the flood used was released (a later
# command completes), and a command whose output fits under the ceiling
# is not flagged - the bound only engages on the output that exceeds it.
$completedBaseline = [int]$env:COMPLETED_BASELINE
$truncatedBaseline = [int]$env:TRUNCATED_BASELINE
$completed = $completedBaseline
for ($i = 1; $i -le 60; $i++) {
  Start-Sleep -Seconds 2
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $completed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
  if ($completed -gt $completedBaseline) { break }
}
if ($completed -le $completedBaseline) {
  Write-Error "No command completed after the flood (count stayed at $completedBaseline): the worker was not released"
  exit 1
}
Write-Output "Worker released; a later command completed (count $completed > baseline $completedBaseline)"

$content = Get-Content $env:LOG_FILE -Raw
$truncated = ([regex]::Matches($content, [regex]::Escape("Command output truncated"))).Count
if ($truncated -ne $truncatedBaseline) {
  Write-Error "A small-output command was flagged as truncated (count rose from $truncatedBaseline to $truncated)"
  exit 1
}
Write-Output "Confirmed small-output command was not truncated (count stayed at $truncatedBaseline)"

