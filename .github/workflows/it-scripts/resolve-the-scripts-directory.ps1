#!/usr/bin/env pwsh
#Requires -Version 7

# Read the directory off a "Command saved to" line rather than
# re-deriving the platform path: the service's temp directory is not the
# runner's, notably root's private TMPDIR on macOS. Many earlier
# scenarios have already logged one.
$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$found = if ($content) { [regex]::Matches($content, 'Command saved to[^\r\n]*path=(?<p>"[^"]*"|\S+)') } else { @() }
if ($found.Count -eq 0) {
  Write-Error "No 'Command saved to' line in the log; cannot resolve the scripts directory"
  exit 1
}
$path = $found[$found.Count - 1].Groups['p'].Value.Trim('"')
$dir = Split-Path -Parent $path
"path=$dir" >> $env:GITHUB_OUTPUT
Write-Output "Scripts directory: $dir"

