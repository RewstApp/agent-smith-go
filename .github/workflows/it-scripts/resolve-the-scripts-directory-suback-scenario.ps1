#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$found = if ($content) { [regex]::Matches($content, 'Command saved to[^\r\n]*path=(?<p>"[^"]*"|\S+)') } else { @() }
if ($found.Count -eq 0) {
  Write-Error "No 'Command saved to' line in the log; cannot resolve the scripts directory"
  exit 1
}
$dir = Split-Path -Parent $found[$found.Count - 1].Groups['p'].Value.Trim('"')
"path=$dir" >> $env:GITHUB_OUTPUT
Write-Output "Scripts directory: $dir"

