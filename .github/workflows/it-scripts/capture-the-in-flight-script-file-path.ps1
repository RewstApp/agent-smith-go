#!/usr/bin/env pwsh
#Requires -Version 7

# The agent logs the temp file it wrote the command to, so read the path
# (and the scripts directory) straight from the log instead of
# re-deriving the platform path - the service's temp directory is not
# the runner's, notably root's private TMPDIR on macOS.
$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $found = if ($content) { [regex]::Matches($content, 'Command saved to[^\r\n]*path=(?<p>"[^"]*"|\S+)') } else { @() }
  if ($found.Count -gt $baseline) {
    $path = $found[$found.Count - 1].Groups['p'].Value.Trim('"')
    $dir = Split-Path -Parent $path
    "path=$path" >> $env:GITHUB_OUTPUT
    "scripts_dir=$dir" >> $env:GITHUB_OUTPUT
    Write-Output "In-flight script file: $path (scripts directory $dir)"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Agent never logged a script file for the long-running command (count stayed at $baseline)"
exit 1

