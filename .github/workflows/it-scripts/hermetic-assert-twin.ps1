# Assert a value in the device twin the stub broker holds for the device
# (sc-129687), polling GET /_control/devices/{id}/twin until the value matches
# or TIMEOUT_SECONDS pass. Env: ENGINE_URL, DEVICE_ID, PROPERTY (a dotted path
# into the reported properties, e.g. "health.journal.degraded"), EXPECTED (the
# value as text: "True"/"False" for booleans, a number, or a string),
# TIMEOUT_SECONDS.
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
function Get-Property($obj, [string]$path) {
  $cur = $obj
  foreach ($part in ($path -split '\.')) {
    if ($null -eq $cur) { return $null }
    $cur = $cur.$part
  }
  return $cur
}
$actual = $null
$lastError = ''
while ($true) {
  try {
    $twin = Invoke-RestMethod -Method Get -Uri "$($env:ENGINE_URL)/_control/devices/$($env:DEVICE_ID)/twin" -TimeoutSec 30
    $actual = Get-Property $twin.reported $env:PROPERTY
    $lastError = ''
  } catch {
    $lastError = $_.Exception.Message
  }
  if ($null -ne $actual -and "$actual" -eq $env:EXPECTED) { break }
  if ((Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
if ($lastError) { Write-Output "last control-surface error: $lastError" }
if ($null -eq $actual -or "$actual" -ne $env:EXPECTED) {
  if ($twin) { Write-Output "reported properties: $($twin.reported | ConvertTo-Json -Compress -Depth 10)" }
  Write-Error "twin property $($env:PROPERTY) is '$actual', expected '$($env:EXPECTED)'"
  exit 1
}
Write-Output "twin property $($env:PROPERTY) = $actual"
