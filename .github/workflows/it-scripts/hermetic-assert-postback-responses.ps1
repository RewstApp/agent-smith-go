# Assert what the stand-in engine saw for one post_id (sc-126322), polling
# GET /_control/postback-responses?post_id=... until the expectation holds or
# TIMEOUT_SECONDS pass. Env: ENGINE_URL, POST_ID, RECORDED ("true": a result
# must be recorded; "false": none may be), MIN_ATTEMPTS (optional floor on the
# postbacks the engine saw, accepted or not), MAX_ATTEMPTS (optional ceiling,
# checked once the recorded expectation holds), MIN_GAP_SECONDS (optional:
# every two consecutive attempts must be at least this far apart, the check
# that no attempt landed inside the Retry-After the engine named, sc-132997),
# TIMEOUT_SECONDS.
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
$wantRecorded = $env:RECORDED -eq 'true'
$minAttempts = if ($env:MIN_ATTEMPTS) { [int]$env:MIN_ATTEMPTS } else { 0 }
$snap = $null
$lastError = ''
while ($true) {
  try {
    $snap = Invoke-RestMethod -Method Get -Uri "$($env:ENGINE_URL)/_control/postback-responses?post_id=$($env:POST_ID)" -TimeoutSec 30
    $lastError = ''
  } catch {
    $snap = $null
    $lastError = $_.Exception.Message
  }
  if ($snap -and ([bool]$snap.recorded) -eq $wantRecorded -and [int]$snap.attempts -ge $minAttempts) { break }
  if ((Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
if (-not $snap) {
  Write-Error "the control surface never answered for $($env:POST_ID): $lastError"
  exit 1
}
Write-Output "$($env:POST_ID): attempts=$($snap.attempts) recorded=$($snap.recorded) remaining=$(@($snap.remaining) -join ',')"
$failures = @()
if (([bool]$snap.recorded) -ne $wantRecorded) {
  $failures += "recorded is $($snap.recorded), expected $wantRecorded"
}
if ([int]$snap.attempts -lt $minAttempts) {
  $failures += "the engine saw $($snap.attempts) postback(s), expected at least $minAttempts"
}
if ($env:MAX_ATTEMPTS -and [int]$snap.attempts -gt [int]$env:MAX_ATTEMPTS) {
  $failures += "the engine saw $($snap.attempts) postback(s), expected at most $($env:MAX_ATTEMPTS)"
}
if ($env:MIN_GAP_SECONDS) {
  $stamps = @($snap.attempts_at | ForEach-Object { [DateTimeOffset]::Parse($_) })
  Write-Output "attempts at: $(($stamps | ForEach-Object { $_.ToString('HH:mm:ss.fff') }) -join ', ')"
  for ($i = 1; $i -lt $stamps.Count; $i++) {
    $gap = ($stamps[$i] - $stamps[$i - 1]).TotalSeconds
    if ($gap -lt [double]$env:MIN_GAP_SECONDS) {
      $failures += "attempt $($i + 1) landed $([math]::Round($gap, 1)) s after attempt $i, inside the $($env:MIN_GAP_SECONDS) s the engine asked for"
    }
  }
}
if ($failures.Count -gt 0) {
  $failures | ForEach-Object { Write-Output "FAIL: $_" }
  Write-Error "the engine's record for $($env:POST_ID) is not what the scenario expects"
  exit 1
}
