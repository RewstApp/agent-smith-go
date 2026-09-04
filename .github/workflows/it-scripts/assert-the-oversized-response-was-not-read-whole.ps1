#!/usr/bin/env pwsh
#Requires -Version 7

# sc-112405: the other half of the oversized-response scenario. The rejection
# message proves the agent noticed the body was too big; this proves it stopped
# reading it. Those are different claims - an implementation that buffered the
# whole body and only then compared its length would produce the identical
# error message while allocating exactly what the ticket is about.
#
# The stub config endpoint logs how many bytes it managed to write before the
# client hung up. A bounded read stops a few megabytes in (the ceiling plus
# whatever the socket buffers had already accepted); an unbounded one takes the
# entire body.
#
# Expects: STUB_LOG, MAX_BYTES_READ, TOTAL_BYTES.

$ErrorActionPreference = 'Stop'

foreach ($required in @('STUB_LOG', 'MAX_BYTES_READ', 'TOTAL_BYTES')) {
  if (-not (Get-Item "env:$required" -ErrorAction SilentlyContinue).Value) {
    throw "$required is required"
  }
}
$maxBytesRead = [int64]$env:MAX_BYTES_READ
$totalBytes = [int64]$env:TOTAL_BYTES

# The hang-up is logged when the endpoint's next write fails, which happens just
# after the agent closes the body - so this can lose a race with the assertion
# that already observed the agent exiting. Polled rather than read once.
$hangup = $null
$fullRead = $null
for ($i = 1; $i -le 30; $i++) {
  if (Test-Path $env:STUB_LOG) {
    $log = Get-Content $env:STUB_LOG -ErrorAction SilentlyContinue | Out-String
    $hangup = [regex]::Match($log, 'client hung up after (\d+) bytes of (\d+)')
    $fullRead = [regex]::Match($log, 'served the full oversized body: (\d+) bytes written')
    if ($hangup.Success -or $fullRead.Success) { break }
  }
  Start-Sleep -Seconds 1
}

Write-Output "---- $($env:STUB_LOG) ----"
if (Test-Path $env:STUB_LOG) { Get-Content $env:STUB_LOG -ErrorAction SilentlyContinue }

$failures = @()

if ($fullRead.Success) {
  # The bug itself: the agent read every byte the endpoint offered.
  $failures += "the agent read the entire $($fullRead.Groups[1].Value)-byte body; the size ceiling did not stop the read"
} elseif (-not $hangup.Success) {
  $failures += "the endpoint logged neither a client hang-up nor a completed body; it may never have been reached"
} else {
  $bytesRead = [int64]$hangup.Groups[1].Value
  $offered = [int64]$hangup.Groups[2].Value
  Write-Output "The endpoint wrote $bytesRead of $offered bytes before the agent hung up"

  # Below the ceiling plus generous room for bytes already in flight in the
  # socket buffers, and far below the body on offer.
  if ($bytesRead -gt $maxBytesRead) {
    $failures += "the endpoint delivered $bytesRead bytes, past the $maxBytesRead-byte allowance for a bounded read"
  }
  # Guards against the fixture having been misconfigured into serving a body
  # small enough that the ceiling was never reached: the whole scenario would
  # then pass without testing anything.
  if ($offered -ne $totalBytes) {
    $failures += "the endpoint offered $offered bytes, not the expected $totalBytes"
  }
  if ($bytesRead -ge $offered) {
    $failures += "the endpoint delivered its entire $offered-byte body"
  }
}

if ($failures.Count -gt 0) {
  $failures | ForEach-Object { Write-Output "FAIL: $_" }
  Write-Error "the oversized response was not bounded on the wire"
  exit 1
}

Write-Output "The oversized body was abandoned mid-stream, as expected"
exit 0
