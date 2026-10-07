#!/usr/bin/env pwsh
#Requires -Version 7

# Poll the stub engine until every expected post_id has been reported, then
# check the shape of what arrived: exactly one interrupted result (the command
# that was executing when the agent was killed), the rest ordinary results, and
# no post_id more than once - the engine refuses duplicates with 400, so a
# second report would show up in the agent log as "Postback already sent" and
# not here (sc-115628's exactly-once boundary, observed from the engine's side).
$count = [int]$env:COUNT
$expected = 1..$count | ForEach-Object { "hermetic-$_" }
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
$all = @()
while ($true) {
  try {
    $all = @(Invoke-RestMethod -Method Get -Uri "$($env:ENGINE_URL)/_control/postbacks" -TimeoutSec 30)
  } catch { $all = @() }
  $seen = @($all | Where-Object { $expected -contains $_.post_id })
  if ($seen.Count -ge $count -or (Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
$seenIds = @($seen | ForEach-Object { $_.post_id } | Sort-Object)
Write-Output "reported: $($seenIds -join ', ')"
$missing = @($expected | Where-Object { $seenIds -notcontains $_ })
if ($missing.Count -gt 0) {
  Write-Error "postbacks never arrived for: $($missing -join ', ')"
  exit 1
}
$interrupted = @($seen | Where-Object { ($_.body | ConvertTo-Json -Compress -Depth 10) -match '"interrupted":\s*true' })
Write-Output "interrupted: $(@($interrupted | ForEach-Object { $_.post_id }) -join ', ')"
if ($interrupted.Count -ne 1) {
  Write-Error "expected exactly one interrupted result (the command running at the kill), got $($interrupted.Count)"
  exit 1
}
Write-Output "all $count commands reported exactly once; one interrupted, $($count - 1) completed"
