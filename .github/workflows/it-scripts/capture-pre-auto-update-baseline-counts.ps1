#!/usr/bin/env pwsh
#Requires -Version 7

$content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
function Get-PatternCount($text, $needle) {
  if ($text) { ([regex]::Matches($text, [regex]::Escape($needle))).Count } else { 0 }
}
$started    = Get-PatternCount $content "Service started"
$subscribed = Get-PatternCount $content "Subscribed to messages"
"started=$started"       >> $env:GITHUB_OUTPUT
"subscribed=$subscribed" >> $env:GITHUB_OUTPUT
Write-Output "Baseline counts -> started=$started subscribed=$subscribed"

