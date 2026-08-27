#!/usr/bin/env pwsh
#Requires -Version 7

# Being verbose is not a failure: the command runs to completion with the
# excess discarded, so it reports "Command completed", not "Command
# failed" or "Command timed out".
$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 30; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Flooding command completed normally (count $count > baseline $baseline)"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "The flooding command never completed (count stayed at $baseline): it was killed for being verbose"
exit 1

