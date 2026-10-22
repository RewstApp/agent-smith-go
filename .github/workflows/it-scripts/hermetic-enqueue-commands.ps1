#!/usr/bin/env pwsh
#Requires -Version 7

# Owe the device COUNT commands through the stub engine's control surface
# without waiting for any result - the way to put work on the queue and then
# kill the agent (sc-117887). Each gets a known post_id (hermetic-N) so the
# postback assertion can check that every one is reported exactly once.
$count = [int]$env:COUNT
$commands = $env:COMMANDS
if (-not $commands -and $env:SCENARIO) {
  . "$PSScriptRoot/lib/E2ECommands.ps1"
  $commands = Get-E2ECommand -Scenario $env:SCENARIO
}
# The matrix passes the command JSON-quoted, as the real engine expects; the
# control surface takes the bare string inside a JSON payload.
try { $commands = $commands | ConvertFrom-Json -ErrorAction Stop } catch { }
# post_id and commands go at the top level so the fixture encodes the script
# the way the real engine does (UTF-16LE, then base64). A raw `payload` would
# be delivered byte-for-byte and the agent would reject the plain text as
# illegal base64 - posting back an error at once instead of running for 30s.
# POST_ID_PREFIX names the post_ids ("<prefix>-<n>"); the kill-and-replay
# scenario keeps the historical "hermetic", the sc-126322 scenarios use one
# prefix per case so the engine-side assertions can tell them apart.
$prefix = if ($env:POST_ID_PREFIX) { $env:POST_ID_PREFIX } else { 'hermetic' }
for ($i = 1; $i -le $count; $i++) {
  $body = @{ device_id = $env:DEVICE_ID; post_id = "$prefix-$i"; commands = $commands } | ConvertTo-Json -Compress
  $resp = Invoke-RestMethod -Method Post -Uri "$($env:ENGINE_URL)/_control/enqueue" -ContentType 'application/json' -Body $body -TimeoutSec 30
  Write-Output "enqueued $prefix-$i as $($resp.id)"
}
