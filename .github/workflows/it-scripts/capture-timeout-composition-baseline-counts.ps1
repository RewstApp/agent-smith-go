#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$subscribed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count } else { 0 }
$truncated  = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command output truncated"))).Count } else { 0 }
$timedOut   = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command timed out"))).Count } else { 0 }
$completed  = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
"subscribed=$subscribed" >> $env:GITHUB_OUTPUT
"truncated=$truncated"   >> $env:GITHUB_OUTPUT
"timed_out=$timedOut"    >> $env:GITHUB_OUTPUT
"completed=$completed"   >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> subscribed=$subscribed truncated=$truncated timed_out=$timedOut completed=$completed"

