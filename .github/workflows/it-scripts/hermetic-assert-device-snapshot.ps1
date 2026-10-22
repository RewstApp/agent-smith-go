# Assert the stub broker's view of the device (sc-126322), polling
# GET /_control/devices/{id} until every given expectation holds or
# TIMEOUT_SECONDS pass. Env: ENGINE_URL, DEVICE_ID, TIMEOUT_SECONDS, and any of
# DELIVERED, INFLIGHT, QUEUED (exact counts) and MIN_PUBACKS (floor).
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
function Test-Snapshot($s) {
  if ($env:DELIVERED -and [int]$s.delivered -ne [int]$env:DELIVERED) { return "delivered=$($s.delivered), expected $($env:DELIVERED)" }
  if ($env:INFLIGHT -and [int]$s.inflight -ne [int]$env:INFLIGHT) { return "inflight=$($s.inflight), expected $($env:INFLIGHT)" }
  if ($env:QUEUED -and [int]$s.queued -ne [int]$env:QUEUED) { return "queued=$($s.queued), expected $($env:QUEUED)" }
  if ($env:MIN_PUBACKS -and [int]$s.pubacks -lt [int]$env:MIN_PUBACKS) { return "pubacks=$($s.pubacks), expected at least $($env:MIN_PUBACKS)" }
  return ''
}
$snap = $null
$problem = 'no snapshot'
while ($true) {
  try {
    $snap = Invoke-RestMethod -Method Get -Uri "$($env:ENGINE_URL)/_control/devices/$($env:DEVICE_ID)" -TimeoutSec 30
    $problem = Test-Snapshot $snap
  } catch {
    $problem = $_.Exception.Message
  }
  if (-not $problem -or (Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
if ($snap) { Write-Output "$($env:DEVICE_ID): $($snap | ConvertTo-Json -Compress)" }
if ($problem) {
  Write-Error "the broker's snapshot of $($env:DEVICE_ID) is not what the scenario expects: $problem"
  exit 1
}
