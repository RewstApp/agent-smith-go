#!/usr/bin/env pwsh
#Requires -Version 7

# The first check fires 30s after the service starts, then the schedule
# is roughly 2s, 4s, 7.5s, 7.5s... Four new lines put two slots at the
# ceiling, which is what the jitter assertion below needs to tell a
# jittered schedule from one that is merely capped, and still leave two
# more slots (~15s) for the recovery flip to land in, so the flip cannot
# race the end of the retry budget.
$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 150; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Retrying update"))).Count } else { 0 }
  if (($count - $baseline) -ge 4) {
    Write-Output "Observed $($count - $baseline) update retries after ~${i}s"
    exit 0
  }
  Start-Sleep -Seconds 1
}
Write-Error "The agent never retried the update against the failing release endpoint"
exit 1

