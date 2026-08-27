#!/usr/bin/env pwsh
#Requires -Version 7

# The agent has just entered a backoff with most of its 6-slot budget
# left (~30s of waiting at the 7.5s cap). A stop that has to wait out
# the pending slot - or worse, the whole budget - delays every caller of
# Stop(): the installer, an upgrade, and an uninstall.
$baseline = [int]$env:BASELINE
$sw = [System.Diagnostics.Stopwatch]::StartNew()
if ($IsWindows) {
  sc.exe stop $env:SERVICE | Out-Null
} elseif ($IsLinux) {
  sudo systemctl stop $env:SERVICE
} else {
  # Bare label, not "system/<label>": stop is a legacy launchctl
  # subcommand that resolves a label in the current domain, and a
  # service-target form is read as a literal label that matches nothing.
  sudo launchctl stop $env:SERVICE
}
# The service managers disagree about exit status for a stop that was
# delivered; the verdict here comes from the agent's own log.
$global:LASTEXITCODE = 0

for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Auto updater stopped"))).Count } else { 0 }
  if ($count -gt $baseline) {
    $sw.Stop()
    $elapsed = [math]::Round($sw.Elapsed.TotalSeconds, 1)
    if ($sw.Elapsed.TotalSeconds -gt 20) {
      Write-Error "The auto updater stopped only after ${elapsed}s, long enough to have waited out its retry schedule"
      exit 1
    }
    Write-Output "Auto updater stopped ${elapsed}s after the stop was issued, mid-backoff"
    exit 0
  }
  Start-Sleep -Milliseconds 500
}
$sw.Stop()
Write-Output "---- last 40 log lines ----"
Get-Content $env:LOG_FILE -Tail 40 -ErrorAction SilentlyContinue
Write-Error "The auto updater never logged a stop after the service was stopped mid-backoff"
exit 1

