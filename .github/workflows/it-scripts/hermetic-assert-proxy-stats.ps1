# Assert what the stub broker's HTTP proxy carried (sc-129691), polling GET
# /_control/proxy until the count for TARGET reaches MIN or TIMEOUT_SECONDS
# pass. Env: ENGINE_URL, KIND ("tunnels" for CONNECT, "forwards" for plain-HTTP
# requests), TARGET (host:port the agent asked for), MIN, TIMEOUT_SECONDS.
if ($env:KIND -notin @('tunnels', 'forwards')) {
  Write-Error "KIND must be tunnels or forwards, got '$($env:KIND)'"
  exit 1
}
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
$count = 0
$stats = $null
while ($true) {
  try {
    $stats = Invoke-RestMethod -Method Get -Uri "$($env:ENGINE_URL)/_control/proxy" -TimeoutSec 30
    $value = $stats.($env:KIND).($env:TARGET)
    $count = if ($null -eq $value) { 0 } else { [int]$value }
  } catch {
    Write-Output "control surface: $($_.Exception.Message)"
  }
  if ($count -ge [int]$env:MIN -or (Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
if ($count -lt [int]$env:MIN) {
  if ($stats) { Write-Output "proxy stats: $($stats | ConvertTo-Json -Compress -Depth 5)" }
  Write-Error "the proxy carried $count $($env:KIND) to $($env:TARGET), expected at least $($env:MIN)"
  exit 1
}
Write-Output "the proxy carried $count $($env:KIND) to $($env:TARGET)"
