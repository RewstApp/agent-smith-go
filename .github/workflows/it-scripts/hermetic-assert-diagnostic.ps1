# Run the agent's --diagnostic mode non-interactively and assert its output
# (sc-131253). The menu reads its choices from standard input, so MENU (the
# choices, comma-separated, e.g. "8,0": show fleet health, then exit) is piped
# in. Env: AGENT_BINARY (path to the agent binary), ORG_ID, MENU, PATTERNS
# (newline-separated substrings that must all appear).
$patterns = @(($env:PATTERNS -split "`r?`n") | Where-Object { $_ })
if ($patterns.Count -eq 0) {
  Write-Error "PATTERNS names no line to look for; an empty assertion would pass whatever --diagnostic printed"
  exit 1
}
$choices = @(($env:MENU -split ',') | ForEach-Object { $_.Trim() } | Where-Object { $_ })
if ($choices.Count -eq 0 -or $choices[-1] -ne '0') {
  Write-Error "MENU must end with 0 (exit), got '$($env:MENU)'"
  exit 1
}
$output = ($choices | & $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic 2>&1 | Out-String)
Write-Output $output
$missing = @()
foreach ($pattern in $patterns) {
  if (-not $output.Contains($pattern)) { $missing += $pattern }
}
if ($missing.Count -gt 0) {
  Write-Error "--diagnostic output lacks: $($missing -join '; ')"
  exit 1
}
Write-Output "--diagnostic output carries every expected line"
