# Run the agent's --diagnostic --json (sc-132994) and assert the document it
# prints: the exit code, the ok of each named check, that the document's own
# verdict agrees with the exit code, and that no secret appears. Env:
# AGENT_BINARY (path to the agent binary), ORG_ID, EXPECT_EXIT (0 when every
# check passed, 2 when one failed), CHECKS (newline-separated name=true or
# name=false, e.g. hub.proxy_443=true) and optionally ABSENT (newline-separated
# substrings that must appear on neither stdout nor stderr, e.g. a proxy
# password). The document is parsed with ConvertFrom-Json, which every
# platform's pwsh has, rather than jq.
$expected = @(($env:CHECKS -split "`r?`n") | ForEach-Object { $_.Trim() } | Where-Object { $_ })
if ($expected.Count -eq 0) {
  Write-Error "CHECKS names no check; an empty assertion would pass whatever --diagnostic --json printed"
  exit 1
}
foreach ($entry in $expected) {
  if ($entry -notmatch '^[a-z0-9_.]+=(true|false)$') {
    Write-Error "CHECKS entry '$entry' is not name=true or name=false"
    exit 1
  }
}
if ($env:EXPECT_EXIT -notmatch '^[0-9]+$') {
  Write-Error "EXPECT_EXIT must be the exit code to expect, got '$($env:EXPECT_EXIT)'"
  exit 1
}

# stdout carries the document alone; stderr is read separately so a line on
# it can never be mistaken for part of the JSON.
$errFile = [IO.Path]::GetTempFileName()
try {
  $stdout = (& $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic --json 2> $errFile | Out-String)
  $code = $LASTEXITCODE
  $stderr = [IO.File]::ReadAllText($errFile)
} finally {
  Remove-Item -Force $errFile -ErrorAction SilentlyContinue
}
Write-Output $stdout
if ($stderr.Trim()) { Write-Output "stderr: $stderr" }

$present = @()
foreach ($absent in @(($env:ABSENT -split "`r?`n") | Where-Object { $_ })) {
  if ($stdout.Contains($absent) -or $stderr.Contains($absent)) { $present += $absent }
}
if ($present.Count -gt 0) {
  Write-Error "--diagnostic --json output must not contain: $($present -join '; ')"
  exit 1
}
if ($code -ne [int]$env:EXPECT_EXIT) {
  Write-Error "--diagnostic --json exited $code, expected $($env:EXPECT_EXIT)"
  exit 1
}
try {
  $doc = $stdout | ConvertFrom-Json -ErrorAction Stop
} catch {
  Write-Error "--diagnostic --json did not print one JSON document: $($_.Exception.Message)"
  exit 1
}
if ([bool]$doc.ok -ne ($code -eq 0)) {
  Write-Error "the document says ok=$($doc.ok) (failed: $($doc.failed -join ', ')) but the exit code is $code"
  exit 1
}
$failures = @()
foreach ($entry in $expected) {
  $name, $want = $entry -split '=', 2
  $found = @($doc.checks | Where-Object { $_.name -eq $name })
  if ($found.Count -ne 1) {
    $failures += "$name appears $($found.Count) time(s)"
    continue
  }
  $got = ([string]$found[0].ok).ToLowerInvariant()
  if ($got -ne $want) {
    $failures += "$name is ok=$got, expected $want ($($found[0].detail))"
  }
}
if ($failures.Count -gt 0) {
  Write-Error "--diagnostic --json: $($failures -join '; ')"
  exit 1
}
Write-Output "--diagnostic --json exited $code with every expected check"
