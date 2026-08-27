#!/usr/bin/env pwsh
#Requires -Version 7

$renewedBaseline = [int]$env:RENEWED_BASELINE
$subscribedBaseline = [int]$env:SUBSCRIBED_BASELINE
for ($i = 1; $i -le 60; $i++) {
  $content = Get-Content $env:LOG_FILE -Raw -ErrorAction SilentlyContinue
  $renewed    = if ($content) { ([regex]::Matches($content, [regex]::Escape("Renewing SAS token before expiry"))).Count } else { 0 }
  $subscribed = if ($content) { ([regex]::Matches($content, [regex]::Escape("Subscribed to messages"))).Count } else { 0 }
  if (($renewed -gt $renewedBaseline) -and ($subscribed -gt $subscribedBaseline)) {
    Write-Output "SAS token renewed (count $renewed > baseline $renewedBaseline) and agent resubscribed (count $subscribed > baseline $subscribedBaseline) after ~$($i * 2)s"
    exit 0
  }
  Start-Sleep -Seconds 2
}
Write-Error "Proactive SAS renewal did not fire and resubscribe (renewed stayed <= $renewedBaseline or subscribed stayed <= $subscribedBaseline)"
exit 1

