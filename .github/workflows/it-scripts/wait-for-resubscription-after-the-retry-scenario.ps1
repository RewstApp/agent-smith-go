#!/usr/bin/env pwsh
#Requires -Version 7

$baseline = [int]$env:BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $count = if ($content) { ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count } else { 0 }
  if ($count -gt $baseline) {
    Write-Output "Agent resubscribed after the retry scenario (count $count > baseline $baseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Agent never resubscribed after the retry scenario"
exit 1

