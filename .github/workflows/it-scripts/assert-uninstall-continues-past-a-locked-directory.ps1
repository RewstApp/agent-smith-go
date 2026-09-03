#!/usr/bin/env pwsh
#Requires -Version 7

# sc-108857: with a file locked inside one installed directory, the uninstall
# must still remove the other directories and name the path it could not remove.
# Before the fix it returned on the first RemoveAll failure, orphaning every
# directory behind it with the service registration already deleted.
#
# Runs the uninstall and asserts on its output and on the state it left behind.
# Expects: BINARY, ORG_ID, SERVICE, LOCKED_DIR, REMOVED_DIRS
# (newline-separated).

$ErrorActionPreference = 'Stop'

$lockedDir = $env:LOCKED_DIR
$removedDirs = @(
  $env:REMOVED_DIRS -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ }
)
if (-not $lockedDir) { throw "LOCKED_DIR is required" }
if ($removedDirs.Count -eq 0) { throw "REMOVED_DIRS is required" }

$out = & "./dist/$($env:BINARY)" --uninstall --org-id $env:ORG_ID 2>&1 | Out-String
# The agent reports failures through its log rather than its exit status, so the
# exit code is reset and the assertions below are what decide this scenario.
$global:LASTEXITCODE = 0
Write-Output "---- uninstaller output ----"
Write-Output $out

$failures = @()

# The registration goes first, so a locked directory must not have prevented it.
if (-not $out.Contains('Service deleted')) {
  $failures += "missing from the log: 'Service deleted'"
}
if (Get-Service -Name $env:SERVICE -ErrorAction SilentlyContinue) {
  $failures += "service registration $($env:SERVICE) survived the uninstall"
}

# The locked directory is reported as failed, by path, in one summary line.
if (-not $out.Contains('Uninstall incomplete; some directories could not be removed')) {
  $failures += "missing from the log: the 'Uninstall incomplete' summary"
}
$summaryLine = ($out -split "`r?`n" |
  Where-Object { $_.Contains('Uninstall incomplete; some directories could not be removed') } |
  Select-Object -First 1)
if ($summaryLine) {
  if (-not $summaryLine.Contains($lockedDir)) {
    $failures += "the summary does not name the locked directory ${lockedDir}: $summaryLine"
  }
  # One of three failed: the fix's whole point is that the other two were still
  # attempted rather than skipped.
  foreach ($pair in @('failed_count=1', 'attempted_count=3')) {
    if (-not $summaryLine.Contains($pair)) {
      $failures += "the summary does not report ${pair}: $summaryLine"
    }
  }
}
if ($out.Contains('Uninstall completed')) {
  $failures += "a partial uninstall reported itself completed"
}

# Every other directory was attempted and removed, both in the log and on disk.
foreach ($dir in $removedDirs) {
  if (-not $out.Contains($dir)) {
    $failures += "the uninstall never mentioned $dir, so it was skipped after the failure"
  }
  if (Test-Path -LiteralPath $dir) {
    $failures += "directory survived the uninstall: $dir"
  }
}

# The locked directory is still there - the honest outcome the summary reported,
# not a silent success.
if (-not (Test-Path -LiteralPath $lockedDir)) {
  $failures += "the locked directory was reported as failed but is gone: $lockedDir"
}

if ($failures.Count -gt 0) {
  $failures | ForEach-Object { Write-Output "FAIL: $_" }
  Write-Error "the uninstall did not continue past the locked directory"
  exit 1
}

Write-Output "Uninstall removed $($removedDirs.Count) directories and named the locked one: $lockedDir"
exit 0
