#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$truncated = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command output truncated"))).Count } else { 0 }
$completed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
"truncated=$truncated" >> $env:GITHUB_OUTPUT
"completed=$completed" >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> truncated=$truncated completed=$completed"

