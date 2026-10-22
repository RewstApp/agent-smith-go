# Seed the command journal the way a previous agent process would have left
# it (sc-126322): one never-started entry for POST_ID running COMMANDS,
# received RECEIVED_AGO_SECONDS ago, plus the clock evidence heartbeat at the
# current wall clock and, when CLOCK_OFFSET is given, the integration build's
# clock offset override file in the data directory. The file layout mirrors
# cmd/agent_smith/command_journal.go: the entry is <hex(sha256(key))[:32]>.json
# with key "post:<post_id>", the payload is the C2D message (commands UTF-16LE
# then base64), and the heartbeat is RFC3339Nano UTC. Run elevated: the data
# directory is owner-only.
# Env: DATA_DIR, POST_ID, COMMANDS, RECEIVED_AGO_SECONDS, CLOCK_OFFSET (optional,
# a Go duration such as "25h" or "-25h"; empty removes the override file).
$ErrorActionPreference = 'Stop'
if (-not $env:DATA_DIR -or -not $env:POST_ID -or -not $env:COMMANDS) {
  Write-Error "DATA_DIR, POST_ID and COMMANDS are required"
  exit 1
}
$journalDir = Join-Path $env:DATA_DIR 'command_journal'
New-Item -ItemType Directory -Force -Path $journalDir | Out-Null

$encoded = [Convert]::ToBase64String([System.Text.Encoding]::Unicode.GetBytes($env:COMMANDS))
$payload = @{ post_id = $env:POST_ID; commands = $encoded } | ConvertTo-Json -Compress
$payloadBytes = [System.Text.Encoding]::UTF8.GetBytes($payload)

$key = "post:$($env:POST_ID)"
$sha = [System.Security.Cryptography.SHA256]::Create()
$digest = $sha.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($key))
$base = (($digest | ForEach-Object { $_.ToString('x2') }) -join '').Substring(0, 32)

$receivedAt = (Get-Date).ToUniversalTime().AddSeconds(-[int]$env:RECEIVED_AGO_SECONDS)
$entry = [ordered]@{
  key         = $key
  payload     = [Convert]::ToBase64String($payloadBytes)
  received_at = $receivedAt.ToString('yyyy-MM-ddTHH:mm:ss.fffffffZ')
}
$entryPath = Join-Path $journalDir "$base.json"
Set-Content -LiteralPath $entryPath -Value ($entry | ConvertTo-Json -Compress) -NoNewline
Write-Output "seeded journal entry $entryPath for $key received at $($entry.received_at)"

$heartbeat = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffffffZ')
Set-Content -LiteralPath (Join-Path $journalDir 'heartbeat') -Value $heartbeat -NoNewline
Write-Output "heartbeat primed at $heartbeat"

$offsetPath = Join-Path $env:DATA_DIR 'clock_offset_override'
if ($env:CLOCK_OFFSET) {
  Set-Content -LiteralPath $offsetPath -Value $env:CLOCK_OFFSET -NoNewline
  Write-Output "clock offset override set to $($env:CLOCK_OFFSET)"
} else {
  Remove-Item -LiteralPath $offsetPath -Force -ErrorAction SilentlyContinue
  Write-Output "clock offset override cleared"
}
