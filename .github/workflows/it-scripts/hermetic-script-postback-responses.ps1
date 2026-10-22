# Script the stand-in engine's answers to one post_id's postbacks (sc-126322):
# POST /_control/postback-responses with the statuses it should answer in
# order before behaving normally. Env: ENGINE_URL, POST_ID, STATUSES
# (comma-separated HTTP statuses, e.g. "503,503,200"; a 200 or the end of the
# list means the normal record-once path).
$statuses = @($env:STATUSES -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ } | ForEach-Object { [int]$_ })
if (-not $env:POST_ID -or $statuses.Count -eq 0) {
  Write-Error "POST_ID and STATUSES are required"
  exit 1
}
$body = @{ post_id = $env:POST_ID; statuses = $statuses } | ConvertTo-Json -Compress
$resp = Invoke-RestMethod -Method Post -Uri "$($env:ENGINE_URL)/_control/postback-responses" -ContentType 'application/json' -Body $body -TimeoutSec 30
Write-Output "engine will answer $($env:POST_ID) with $($statuses -join ', ') (attempts so far: $($resp.attempts))"
