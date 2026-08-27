#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw
$subscribed = ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count
$completed  = ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count
"subscribed=$subscribed" >> $env:GITHUB_OUTPUT
"completed=$completed"   >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> subscribed=$subscribed completed=$completed"

