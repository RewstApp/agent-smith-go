#!/usr/bin/env pwsh
#Requires -Version 7

# Holds an exclusive handle on LockFile until ReleaseFile appears, so a
# RemoveAll of the directory containing it fails with a sharing violation (see
# the locked-file action's description). Run detached by that action; not meant
# to be invoked directly.
#
# Everything arrives as an argument rather than through the environment: the
# holder is launched with Win32_Process.Create, which gives the new process the
# WMI provider host's environment, not the launching step's.

param(
  [Parameter(Mandatory = $true)][string]$LockFile,
  [Parameter(Mandatory = $true)][string]$ReadyFile,
  [Parameter(Mandatory = $true)][string]$ReleaseFile,
  [Parameter(Mandatory = $true)][string]$LogFile,
  # Bounded so a leaked holder cannot leave the directory unremovable for the
  # rest of the job; a scenario releases it explicitly long before this.
  [int]$MaxLifetimeMinutes = 10
)

$ErrorActionPreference = 'Stop'

try {
  # FileShare::None withholds FILE_SHARE_DELETE, which is what makes the unlink
  # itself fail rather than merely a second open.
  $handle = [System.IO.File]::Open(
    $LockFile,
    [System.IO.FileMode]::Open,
    [System.IO.FileAccess]::Read,
    [System.IO.FileShare]::None
  )
  "[$(Get-Date -Format o)] handle open on $LockFile (pid $PID)" |
    Out-File -FilePath $LogFile -Encoding utf8

  # Written only once the handle is actually open, so the launching step can
  # wait on evidence rather than on a sleep.
  Set-Content -LiteralPath $ReadyFile -Value $PID -NoNewline

  # Polls for a release file rather than taking a signal, matching the
  # wedged-service fixture.
  $deadline = (Get-Date).AddMinutes($MaxLifetimeMinutes)
  while (-not (Test-Path -LiteralPath $ReleaseFile) -and (Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 250
  }

  $handle.Close()
  $reason = if (Test-Path -LiteralPath $ReleaseFile) {
    'released'
  } else {
    "lifetime of ${MaxLifetimeMinutes}m elapsed"
  }
  "[$(Get-Date -Format o)] handle closed ($reason)" |
    Out-File -FilePath $LogFile -Append -Encoding utf8
  exit 0
} catch {
  "[$(Get-Date -Format o)] holder failed: $_" |
    Out-File -FilePath $LogFile -Append -Encoding utf8
  exit 1
}
