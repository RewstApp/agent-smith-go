#!/usr/bin/env pwsh
#Requires -Version 7

Write-Output "---- last 100 lines of $($env:LOG_FILE) ----"
Get-Content $env:LOG_FILE -Tail 100 -ErrorAction SilentlyContinue

