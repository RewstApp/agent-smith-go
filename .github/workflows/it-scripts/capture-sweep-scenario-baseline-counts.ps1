#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$subscribed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count } else { 0 }
$saved      = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command saved to"))).Count } else { 0 }
"subscribed=$subscribed" >> $env:GITHUB_OUTPUT
"saved=$saved"           >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> subscribed=$subscribed saved=$saved"

