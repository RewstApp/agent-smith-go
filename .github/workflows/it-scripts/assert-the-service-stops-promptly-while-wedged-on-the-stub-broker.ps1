#!/usr/bin/env pwsh
#Requires -Version 7

function Get-StopCount {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  if ($content) { ([regex]::Matches($content, [regex]::Escape("Service stopped"))).Count } else { 0 }
}

# Counted here rather than reusing the scenario-wide baseline: the
# --update that pointed the agent at the stub broker restarts the
# service, and that restart logs its own "Service stopped". Measured
# against the older baseline the delta was already satisfied before this
# stop was even issued, so the assertion "passed" in 0.2s without
# observing the stop it exists to test.
$before = Get-StopCount
Write-Output "Service stopped lines before issuing the stop: $before"

$sw = [System.Diagnostics.Stopwatch]::StartNew()

if ($IsWindows) {
  sc.exe stop $env:SERVICE | Out-Null
} elseif ($IsLinux) {
  sudo systemctl stop $env:SERVICE
} else {
  # Bare label, not a "system/<label>" service target. launchctl's
  # `stop` is a legacy subcommand that resolves a label in the current
  # domain, so the target form is taken as a literal label, matches
  # nothing, and exits 3 (ESRCH) without ever signalling the agent.
  # This mirrors what the agent's own darwinService.Stop does. `stop`
  # leaves the job loaded, so the later start-service can run it again.
  sudo launchctl stop $env:SERVICE
}

# launchctl exits non-zero in situations that are not failures here -
# the repo's own stop-service action already guards it with `|| true` -
# and the pwsh shell ends by exiting with $LASTEXITCODE, which failed
# this step on macOS even after the assertion below had passed. Report
# the code and clear it; whether the stop was honored is decided by the
# agent's log, not by the service manager's exit status.
Write-Output "Stop command exit code: $LASTEXITCODE"
$global:LASTEXITCODE = 0

# A clean exit runs the agent's deferred teardown, whose last act is the
# "Service stopped" line; a force-kill past the stop deadline never
# produces one.
$stopped = $false
while ($sw.Elapsed.TotalSeconds -lt 30) {
  if ((Get-StopCount) -gt $before) { $stopped = $true; break }
  Start-Sleep -Milliseconds 500
}
$sw.Stop()

if (-not $stopped) {
  # Both halves matter when this fails: the agent log says whether the
  # stop was received and ignored, and the service manager says whether
  # it was ever delivered.
  Write-Output "---- last 60 lines of the agent log ----"
  Get-Content $env:LOG_FILE -Tail 60 -ErrorAction SilentlyContinue
  Write-Output "---- service manager state ----"
  if ($IsWindows) {
    sc.exe query $env:SERVICE
  } elseif ($IsLinux) {
    sudo systemctl status $env:SERVICE --no-pager
  } else {
    # print, unlike stop, is a modern subcommand and does take the
    # system/<label> service target.
    sudo launchctl print "system/$($env:SERVICE)"
  }
  $global:LASTEXITCODE = 0
  Write-Error "Service did not log a clean stop within 30s while wedged on a withholding broker"
  exit 1
}
Write-Output "Service stopped cleanly after $([math]::Round($sw.Elapsed.TotalSeconds, 1))s while wedged"
exit 0

