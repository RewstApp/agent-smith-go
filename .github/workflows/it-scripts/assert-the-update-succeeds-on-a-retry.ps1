#!/usr/bin/env pwsh
#Requires -Version 7

# The fixture serves the running agent's own version as tag_name, so a
# recovered check ends at "No updates available" and the retry is seen
# succeeding without downloading or executing anything - the recovery is
# what is under test here, not the installer.
$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Update succeeded on retry"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Update succeeded on a retry after ~$($i * 2)s (count $count > baseline $baseline)"
    exit 0
  }
  Start-Sleep -Seconds 2
}

# Separates a real regression from a flip that landed after the budget
# was already spent, which would leave the next check succeeding on its
# first try and logging no retry at all.
$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
if ($content -and $content.Contains("All retries exhausted")) {
  Write-Output "Note: the log records an exhausted retry budget; the recovery flip may have landed after the last slot"
}
Write-Output "---- last 40 log lines ----"
Get-Content $env:LOG_FILE -Tail 40 -ErrorAction SilentlyContinue
Write-Error "The agent never logged a successful update retry after the endpoint recovered"
exit 1

