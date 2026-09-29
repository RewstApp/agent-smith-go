#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$count = if ($content) { ([regex]::Matches($content, [regex]::Escape($env:PATTERN))).Count } else { 0 }
"count=$count" >> $env:GITHUB_OUTPUT
Write-Output "Baseline for '$($env:PATTERN)': $count"
