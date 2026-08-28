#!/usr/bin/env pwsh
#Requires -Version 7

# Pins the sc-108853 fix: the child spawned via Start-Process must not survive
# the parent shell's timeout kill. Before the job object, Process.Kill on
# Windows only reaped the immediate shell process, leaving this child running
# indefinitely.
for ($i = 1; $i -le 10; $i++) {
  $match = Get-CimInstance Win32_Process -Filter "CommandLine like '%91743%'" -ErrorAction SilentlyContinue
  if (-not $match) {
    Write-Output "Child process is gone (checked after ~${i}s)"
    exit 0
  }
  Start-Sleep -Seconds 1
}
Write-Error "Child process (pid $($match[0].ProcessId)) survived the parent's timeout kill - the descendant tree was not torn down"
exit 1
