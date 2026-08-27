#!/usr/bin/env pwsh
#Requires -Version 7

$baseTimedOut   = [int]$env:BASE_TIMED_OUT
$baseBackoffs   = [int]$env:BASE_BACKOFFS
$baseSubscribed = [int]$env:BASE_SUBSCRIBED
$needle = "Failed to subscribe: timed out waiting for broker acknowledgement"

# Two wedged cycles plus their backoff waits take roughly 10+2+10+4s.
$content = ""
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $timedOut = if ($content) { ([regex]::Matches($content, [regex]::Escape($needle))).Count } else { 0 }
  $backoffs = if ($content) { ([regex]::Matches($content, [regex]::Escape("Reconnecting in"))).Count } else { 0 }
  if (($timedOut - $baseTimedOut) -ge 2 -and ($backoffs - $baseBackoffs) -ge 2) { break }
  Start-Sleep -Seconds 2
}

$timedOut = if ($content) { ([regex]::Matches($content, [regex]::Escape($needle))).Count } else { 0 }
if (($timedOut - $baseTimedOut) -lt 2) {
  Write-Error "Expected at least two bounded subscribe timeouts, got $($timedOut - $baseTimedOut)"
  exit 1
}
Write-Output "Bounded subscribe timeouts observed: $($timedOut - $baseTimedOut)"

# The agent must not have subscribed: a withheld SUBACK means no
# subscription, and a "Subscribed to messages" line here would mean the
# stub broker is not the broker the agent reached.
$subscribed = ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count
if ($subscribed -ne $baseSubscribed) {
  Write-Error "Agent reported a subscription against a broker that withholds SUBACK"
  exit 1
}

# The backoff must not be cleared by a timed-out subscribe: a throttling
# broker needs progressively longer waits, not a tight reconnect loop.
# Go renders a duration as concatenated unit components ("4.1s",
# "1m4s"), so each component is summed rather than parsed as one number.
function ConvertTo-Seconds([string]$value) {
  $total = 0.0
  foreach ($m in [regex]::Matches($value, '(?<n>[0-9.]+)(?<u>ms|us|ns|h|m|s)')) {
    $n = [double]$m.Groups['n'].Value
    switch ($m.Groups['u'].Value) {
      'ns' { $total += $n / 1e9 }
      'us' { $total += $n / 1e6 }
      'ms' { $total += $n / 1e3 }
      's'  { $total += $n }
      'm'  { $total += $n * 60 }
      'h'  { $total += $n * 3600 }
    }
  }
  return $total
}

$waits = [regex]::Matches($content, 'Reconnecting in: timeout=(?<t>[0-9.a-z]+)') |
  ForEach-Object { ConvertTo-Seconds $_.Groups['t'].Value }
$new = @($waits | Select-Object -Skip $baseBackoffs)
if ($new.Count -lt 2) {
  Write-Error "Expected at least two reconnect waits after the subscribe timeouts, got $($new.Count)"
  exit 1
}
Write-Output "Reconnect waits after the subscribe timeouts: $($new -join ', ')"
if ($new[1] -le $new[0]) {
  Write-Error "Reconnect backoff did not grow ($($new[0]) then $($new[1])); a timed-out subscribe must not clear it"
  exit 1
}
Write-Output "Confirmed the reconnect backoff grows after a withheld SUBACK"

