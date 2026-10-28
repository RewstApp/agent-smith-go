# Set desired properties on the device twin the stub broker holds (sc-129688):
# POST DESIRED (a JSON object; null removes a key) to
# /_control/devices/{id}/desired. The broker merges it, bumps $version and
# delivers it to the agent as a desired-properties PATCH when the agent is
# listening; otherwise the agent reads it with its next twin GET.
# Env: ENGINE_URL, DEVICE_ID, DESIRED.
try {
  $null = $env:DESIRED | ConvertFrom-Json -ErrorAction Stop
} catch {
  Write-Error "DESIRED is not a JSON object: $($_.Exception.Message)"
  exit 1
}
$resp = Invoke-RestMethod -Method Post -Uri "$($env:ENGINE_URL)/_control/devices/$($env:DEVICE_ID)/desired" -ContentType 'application/json' -Body $env:DESIRED -TimeoutSec 30
Write-Output "desired properties of $($env:DEVICE_ID) at `$version $($resp.version): $($resp.desired | ConvertTo-Json -Compress -Depth 5)"
