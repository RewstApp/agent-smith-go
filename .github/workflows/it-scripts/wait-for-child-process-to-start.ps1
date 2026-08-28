#!/usr/bin/env pwsh
#Requires -Version 7

# Confirms the child spawned by the child-process-kill scenario (sc-108853)
# actually launched before the parent's timeout fires, so a later "child is
# gone" assertion proves the job object killed it rather than the child
# simply never having started.
for ($i = 1; $i -le 15; $i++) {
  $match = Get-CimInstance Win32_Process -Filter "CommandLine like '%91743%'" -ErrorAction SilentlyContinue
  if ($match) {
    Write-Output "Child process started (pid $($match[0].ProcessId)) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Child process never appeared in the process list; the scenario cannot verify anything was killed"
exit 1
