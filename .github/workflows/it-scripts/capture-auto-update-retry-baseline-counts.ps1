#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$retrying  = if ($content) { ([regex]::Matches($content, [regex]::Escape("Retrying update"))).Count } else { 0 }
$succeeded = if ($content) { ([regex]::Matches($content, [regex]::Escape("Update succeeded on retry"))).Count } else { 0 }
"retrying=$retrying"   >> $env:GITHUB_OUTPUT
"succeeded=$succeeded" >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> retrying=$retrying succeeded=$succeeded"

