# Assert a value in the device twin the stub broker holds for the device
# (sc-129687), polling GET /_control/devices/{id}/twin until the value matches
# or TIMEOUT_SECONDS pass. Env: ENGINE_URL, DEVICE_ID, PROPERTY (a dotted path
# into the reported properties, e.g. "health.journal.degraded"; a segment may
# index an array, "commands.in_flight[0].post_id", or be "Count" for an array's
# length), and either EXPECTED (the value as text: "True"/"False" for booleans,
# a number, or a string) or MIN (a numeric floor the value must reach,
# sc-129689), plus TIMEOUT_SECONDS.
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
function Get-Property($obj, [string]$path) {
  $cur = $obj
  foreach ($part in ($path -split '\.')) {
    if ($null -eq $cur) { return $null }
    if ($part -match '^(?<name>[^\[]+)\[(?<index>\d+)\]$') {
      $arr = @($cur.($Matches['name']))
      $i = [int]$Matches['index']
      if ($i -ge $arr.Count) { return $null }
      $cur = $arr[$i]
    } elseif ($part -eq 'Count') {
      $cur = @($cur).Count
    } else {
      $cur = $cur.$part
    }
  }
  return $cur
}
function Test-Expectation($value) {
  if ($null -eq $value) { return $false }
  if ($env:MIN) { return ([double]$value -ge [double]$env:MIN) }
  return ("$value" -eq $env:EXPECTED)
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
  if (Test-Expectation $actual) { break }
  if ((Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
if ($lastError) { Write-Output "last control-surface error: $lastError" }
$want = if ($env:MIN) { "at least $($env:MIN)" } else { "'$($env:EXPECTED)'" }
if (-not (Test-Expectation $actual)) {
  if ($twin) { Write-Output "reported properties: $($twin.reported | ConvertTo-Json -Compress -Depth 10)" }
  Write-Error "twin property $($env:PROPERTY) is '$actual', expected $want"
  exit 1
}
Write-Output "twin property $($env:PROPERTY) = $actual (expected $want)"
