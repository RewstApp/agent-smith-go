#!/usr/bin/env pwsh
#Requires -Version 7

# Builds a special integration test binary with:
# - version overridden to 0.0.0-it (older than any real release, forces auto-update)
# - updateIntervalStr overridden to 30s (triggers update check quickly)
# - sasTokenLifetimeOverrideStr overridden to 90s (proactive SAS token renewal
#   fires ~30s in - lifetime minus the ~60s renew margin - so the renewal path
#   can be exercised in seconds not hours)
# - stopTimeoutOverrideStr overridden to 25s (the bounded wait for a Windows
#   service to reach Stopped, so the wedged-service scenario observes the update
#   abort in seconds instead of the production five minutes). The symbol only
#   exists in the Windows build; -X against a missing symbol is ignored, so the
#   same flag is harmless on Linux and macOS.
# - exitTimeoutOverrideStr overridden to 25s (the bounded wait for the old agent
#   process to actually exit before its files are replaced or deleted, so a
#   process that never exits is given up on in seconds instead of the production
#   two minutes)

$env:GOARCH = "amd64"

$versionFlag = "-X github.com/RewstApp/agent-smith-go/internal/version.Version=v0.0.0-it"
$intervalFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.updateIntervalStr=30s"
$baseBackoffFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.baseBackoffStr=10s"
$maxRetriesFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.maxRetriesStr=6"
$sasTokenLifetimeFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.sasTokenLifetimeOverrideStr=90s"
$stopTimeoutFlag = "-X github.com/RewstApp/agent-smith-go/internal/service.stopTimeoutOverrideStr=25s"
$exitTimeoutFlag = "-X main.exitTimeoutOverrideStr=25s"
$ldflags = "-w -s $versionFlag $intervalFlag $baseBackoffFlag $maxRetriesFlag $sasTokenLifetimeFlag $stopTimeoutFlag $exitTimeoutFlag"

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
