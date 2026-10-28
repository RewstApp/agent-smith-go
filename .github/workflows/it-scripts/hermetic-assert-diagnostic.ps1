# Run the agent's --diagnostic mode non-interactively and assert its output
# (sc-131253). The menu reads its choices from standard input, so MENU (the
# choices, comma-separated, e.g. "8,0": show fleet health, then exit) is piped
# in. Env: AGENT_BINARY (path to the agent binary), ORG_ID, MENU, PATTERNS
# (newline-separated substrings that must all appear).
$choices = $env:MENU -split ','
$output = ($choices | & $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic 2>&1 | Out-String)
Write-Output $output
$missing = @()
foreach ($pattern in ($env:PATTERNS -split "`r?`n")) {
  if ($pattern -and -not $output.Contains($pattern)) { $missing += $pattern }
}
if ($missing.Count -gt 0) {
  Write-Error "--diagnostic output lacks: $($missing -join '; ')"
  exit 1
}
Write-Output "--diagnostic output carries every expected line"
