# Assert a log line appeared exactly EXPECTED more times than BASELINE
# (sc-126322: the journal's once-per-transition degradation and recovery lines,
# the clock's once-per-cycle backward-step Error). Polls until the count
# reaches the expectation or TIMEOUT_SECONDS pass, then fails on more as well
# as fewer. Env: LOG_FILE, PATTERN, EXPECTED, BASELINE, TIMEOUT_SECONDS.
$baseline = [int]$env:BASELINE
$expected = [int]$env:EXPECTED
$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
$count = 0
while ($true) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape($env:PATTERN))).Count } else { 0 }
  if (($count - $baseline) -ge $expected -or (Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 1
}
$seen = $count - $baseline
if ($seen -ne $expected) {
  Write-Output "---- last 40 log lines ----"
  Get-Content $env:LOG_FILE -Tail 40 -ErrorAction SilentlyContinue
  Write-Error "'$($env:PATTERN)' appeared $seen time(s) past the baseline of $baseline, expected exactly $expected"
  exit 1
}
Write-Output "'$($env:PATTERN)' appeared exactly $expected time(s) past the baseline of $baseline"
