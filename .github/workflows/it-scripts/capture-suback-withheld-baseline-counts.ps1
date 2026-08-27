#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
function Get-PatternCount($text, $needle) {
  if ($text) { ([regex]::Matches($text, [regex]::Escape($needle))).Count } else { 0 }
}
$subscribed = Get-PatternCount $content "Subscribed to messages"
$timedOut   = Get-PatternCount $content "Failed to subscribe: timed out waiting for broker acknowledgement"
$backoffs   = Get-PatternCount $content "Reconnecting in"
"subscribed=$subscribed" >> $env:GITHUB_OUTPUT
"timed_out=$timedOut"    >> $env:GITHUB_OUTPUT
"backoffs=$backoffs"     >> $env:GITHUB_OUTPUT
Write-Output "Baseline -> subscribed=$subscribed timed_out=$timedOut backoffs=$backoffs"

