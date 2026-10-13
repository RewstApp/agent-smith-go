#!/usr/bin/env pwsh
#Requires -Version 7

# The agent logs the temp file it wrote the command to, so read the path
# (and the scripts directory) straight from the log instead of
# re-deriving the platform path - the service's temp directory is not
# the runner's, notably root's private TMPDIR on macOS.
#
# Not simply the newest line past the baseline: a fresh install draws
# commands from outside the scenario (run 38442590289 saw a foreign command
# arrive 66 s after the long-running one, exit, and clean its script up), and
# the newest path is then a file that no longer exists. The in-flight script
# is the one that is still on disk and contains the command this scenario
# sent, so the candidates are matched on content (sc-126317).
$baseline = [int]$env:BASELINE
$command = $env:COMMAND
try { $command = $command | ConvertFrom-Json -ErrorAction Stop } catch { }
if (-not $command) {
  Write-Error "COMMAND is required: the script file is identified by the command it holds"
  exit 1
}
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $found = if ($content) { [regex]::Matches($content, 'Command saved to[^\r\n]*path=(?<p>"[^"]*"|\S+)') } else { @() }
  if ($found.Count -gt $baseline) {
    for ($k = $found.Count - 1; $k -ge $baseline; $k--) {
      $path = $found[$k].Groups['p'].Value.Trim('"')
      if (-not (Test-Path -LiteralPath $path)) { continue }
      $script = Get-Content -LiteralPath $path -Raw -ErrorAction SilentlyContinue
      if ($script -and $script.Contains($command)) {
        $dir = Split-Path -Parent $path
        "path=$path" >> $env:GITHUB_OUTPUT
        "scripts_dir=$dir" >> $env:GITHUB_OUTPUT
        Write-Output "In-flight script file: $path (scripts directory $dir)"
        exit 0
      }
    }
  }
  Start-Sleep -Seconds 2
}
Write-Error "Agent never logged a still-present script file containing the long-running command (saved-to count $($found.Count), baseline $baseline)"
exit 1
