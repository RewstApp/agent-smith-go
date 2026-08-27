#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
$timedOut  = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command timed out"))).Count } else { 0 }
$completed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Command completed"))).Count } else { 0 }
"timed_out=$timedOut" >> $env:GITHUB_OUTPUT
"completed=$completed" >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> timed_out=$timedOut completed=$completed"

