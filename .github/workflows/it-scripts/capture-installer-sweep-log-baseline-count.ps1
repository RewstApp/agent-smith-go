#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$swept = if ($content) { ([regex]::Matches($content, [regex]::Escape("Swept stale installer files"))).Count } else { 0 }
"swept=$swept" >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> swept=$swept"

