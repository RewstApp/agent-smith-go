#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$truncated = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command output truncated"))).Count } else { 0 }
"truncated=$truncated" >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> truncated=$truncated"

