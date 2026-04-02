#!/usr/bin/env pwsh
#Requires -Version 7

# Builds a special integration test binary with:
# - version overridden to 0.0.0-it (older than any real release, forces auto-update)
# - updateIntervalStr overridden to 30s (triggers update check quickly)

$env:GOARCH = "amd64"

$versionFlag = "-X github.com/RewstApp/agent-smith-go/internal/version.Version=v0.0.0-it"
$intervalFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.updateIntervalStr=30s"
$baseBackoffFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.baseBackoffStr=10s"
$maxRetriesFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.maxRetriesStr=6"
$ldflags = "-w -s $versionFlag $intervalFlag $baseBackoffFlag $maxRetriesFlag"

if ($IsWindows) {
    $buildOutput = "./dist/rewst_agent_config.win.it.exe"
    $env:GOOS = "windows"
    go build -ldflags="$ldflags" -o $buildOutput "./cmd/agent_smith"
    Write-Output $buildOutput
}

if ($IsLinux) {
    $buildOutput = "./dist/rewst_agent_config.linux.it.bin"
    $env:GOOS = "linux"
    go build -ldflags="$ldflags" -o $buildOutput "./cmd/agent_smith"
    Write-Output $buildOutput
}

if ($IsMacOS) {
    $buildOutput = "./dist/rewst_agent_config.mac-os.it.bin"
    $env:GOOS = "darwin"
    go build -ldflags="$ldflags" -o $buildOutput "./cmd/agent_smith"
    Write-Output $buildOutput
}
