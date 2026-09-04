#!/usr/bin/env pwsh
#Requires -Version 7

# sc-112405: config mode read the config endpoint's response with a bare
# io.ReadAll. configHTTPTimeout (5 minutes) bounded the request in time but not
# in bytes, so an endpoint answering 200 with an effectively endless body drove
# unbounded allocation during install. The body is now read through an
# io.LimitReader capped at maxConfigResponseSize (10 MiB).
#
# Runs config mode against the stub config endpoint while it is serving the
# oversized body, and asserts the install aborts with the size error, fast, and
# without having started to install anything.
#
# Expects: BINARY, ORG_ID, CONFIG_URL, CONFIG_SECRET, MAX_BYTES,
# MAX_ELAPSED_SECONDS.

$ErrorActionPreference = 'Stop'

foreach ($required in @('BINARY', 'ORG_ID', 'CONFIG_URL', 'CONFIG_SECRET', 'MAX_BYTES')) {
  if (-not (Get-Item "env:$required" -ErrorAction SilentlyContinue).Value) {
    throw "$required is required"
  }
}
$maxElapsed = [double]($env:MAX_ELAPSED_SECONDS ? $env:MAX_ELAPSED_SECONDS : 120)

$binary = "./dist/$($env:BINARY)"
$agentArgs = @(
  '--config-url', $env:CONFIG_URL,
  '--config-secret', $env:CONFIG_SECRET,
  '--org-id', $env:ORG_ID
)

$sw = [System.Diagnostics.Stopwatch]::StartNew()
# Config mode writes to the system data and program directories, so it needs
# elevation on Unix; the Windows runner is already elevated.
if ($IsWindows) {
  $out = & $binary @agentArgs 2>&1 | Out-String
} else {
  $out = & sudo $binary @agentArgs 2>&1 | Out-String
}
$exitCode = $LASTEXITCODE
$sw.Stop()
# The assertions below are this scenario's verdict, so a non-zero exit here -
# which is the expected outcome - must not fail the step on its own.
$global:LASTEXITCODE = 0

$elapsed = [math]::Round($sw.Elapsed.TotalSeconds, 1)
Write-Output "---- config mode output after ${elapsed}s (exit $exitCode) ----"
Write-Output $out

$failures = @()

# Unlike the auto-updater, config mode reports failure through its exit status:
# it is an interactive, one-shot install, and the installer script that calls it
# needs to know the endpoint was never configured.
if ($exitCode -eq 0) {
  $failures += "config mode exited 0 on an oversized response; it must fail the install"
}

# The error has to name the ceiling, since it is the only account an operator
# gets of why an install against a working, 200-answering endpoint refused to
# proceed.
foreach ($needle in @(
  'config error:',
  "configuration response exceeds maximum allowed size of $($env:MAX_BYTES) bytes"
)) {
  if (-not $out.Contains($needle)) { $failures += "missing from the output: '$needle'" }
}

# Rejection happens before the body is unmarshalled, so the failure must not
# surface as a parse error on a truncated prefix - that would mean the ceiling
# cut the body off and the agent then tried to use what it had.
foreach ($absent in @('failed to parse response', 'invalid configuration')) {
  if ($out.Contains($absent)) {
    $failures += "the oversized body was processed rather than rejected outright: '$absent'"
  }
}

# And it must not have carried on into the install proper.
foreach ($absent in @(
  'Received configuration',
  'Configuration saved to',
  'Agent installed to',
  'Service created',
  'Service started'
)) {
  if ($out.Contains($absent)) { $failures += "the install proceeded past the oversized response: '$absent'" }
}

# The headline distinction: it aborted on bytes, not by riding out
# configHTTPTimeout. A read that ran to the 5-minute deadline would look like a
# pass to every assertion above while leaving the memory exposure wide open.
if ($sw.Elapsed.TotalSeconds -gt $maxElapsed) {
  $failures += "config mode took ${elapsed}s, long enough to have been ended by configHTTPTimeout rather than the size ceiling"
}

if ($failures.Count -gt 0) {
  $failures | ForEach-Object { Write-Output "FAIL: $_" }
  Write-Error "config mode did not reject the oversized response cleanly"
  exit 1
}

Write-Output "Config mode rejected the oversized response after ${elapsed}s, as expected"
exit 0
