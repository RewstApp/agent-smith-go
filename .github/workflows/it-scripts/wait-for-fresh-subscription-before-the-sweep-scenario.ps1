#!/usr/bin/env pwsh
#Requires -Version 7

# A delta, not a bare match: the log already carries many "Subscribed to
# messages" lines from earlier scenarios, so only a new one proves the
# restarted agent is connected and ready to receive the command.
$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Agent subscribed after the sweep-scenario install (count $count > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Agent did not subscribe after the sweep-scenario install (count stayed at $baseline)"
exit 1

