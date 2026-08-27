#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$renewed    = if ($content) { ([regex]::Matches($content, [regex]::Escape("Renewing SAS token before expiry"))).Count } else { 0 }
$subscribed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count } else { 0 }
$lost       = if ($content) { ([regex]::Matches($content, [regex]::Escape("Connection lost"))).Count } else { 0 }
$completed  = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
"renewed=$renewed"       >> $env:GITHUB_OUTPUT
"subscribed=$subscribed" >> $env:GITHUB_OUTPUT
"lost=$lost"             >> $env:GITHUB_OUTPUT
"completed=$completed"   >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> renewed=$renewed subscribed=$subscribed lost=$lost completed=$completed"

