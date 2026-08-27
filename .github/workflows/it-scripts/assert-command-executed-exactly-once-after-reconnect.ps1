#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
$observed = $baseline
# Wait for the post-reconnect command to complete.
for ($i = 1; $i -le 30; $i++) {
  Start-Sleep -Seconds 2
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $observed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
  if ($observed -gt $baseline) { break }
}
if (($observed - $baseline) -lt 1) {
  Write-Error "Post-reconnect command never completed (count stayed at $baseline)"
  exit 1
}
# Allow a brief window for any duplicate delivery to surface, then
# require the delta to be exactly one - guarding against re-delivery of
# the buffered message on a non-clean reconnected session.
Start-Sleep -Seconds 10
$content = Get-Content $env:LOG_FILE -Raw
$final = ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count
$delta = $final - $baseline
Write-Output "Post-reconnect 'Command completed' delta: $delta (baseline $baseline, final $final)"
if ($delta -ne 1) {
  Write-Error "Expected exactly one post-reconnect 'Command completed', got $delta (duplicate delivery?)"
  exit 1
}
Write-Output "Confirmed exactly one post-reconnect command execution"

