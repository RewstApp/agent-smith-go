#!/usr/bin/env pwsh
#Requires -Version 7

# Builds a special integration test binary with:
# - version overridden to 0.0.0-it (older than any real release, forces auto-update)
# - updateIntervalStr overridden to 30s (triggers update check quickly)
# - sasTokenLifetimeOverrideStr overridden to 90s (proactive SAS token renewal
#   fires ~30s in - lifetime minus the ~60s renew margin - so the renewal path
#   can be exercised in seconds not hours)
# - stopTimeoutOverrideStr overridden to 25s (the bounded wait for a Windows
#   service to reach Stopped, so the wedged-service scenario (test/e2e) observes the update
#   abort in seconds instead of the production five minutes). The symbol only
#   exists in the Windows build; -X against a missing symbol is ignored, so the
#   same flag is harmless on Linux and macOS.
# - exitTimeoutOverrideStr overridden to 25s (the bounded wait for the old agent
#   process to actually exit before its files are replaced or deleted, so a
#   process that never exits is given up on in seconds instead of the production
#   two minutes)
# - baseBackoffStr overridden to 2s and maxRetriesStr to 6, so a full update
#   retry sequence runs in well under a minute. With the 30s check interval the
#   retry cap is a quarter of it (7.5s), so the schedule doubles 2s, 4s, then
#   flattens at the cap - both halves of the sc-106110 fix are observable in one
#   sequence
# - releaseUrlOverrideFileStr overridden to release_url_override, which lets the
#   integration harness point the auto-updater at a local stub release endpoint
#   (see test/e2e's SetReleaseURL) so the retry schedule can be driven by
#   an endpoint that fails on demand. Released builds leave this empty and never
#   look for the file, so their update source stays fixed at build time
# - defaultCommandTimeoutOverrideStr overridden to 5s, so the "hung command
#   killed by the default per-command timeout" scenario (sc-108852) can be
#   exercised in seconds instead of the production 30 minutes. It only replaces
#   the fallback used when command_timeout_seconds is left unconfigured; it
#   never applies once a device explicitly configures the field.
# - launchctlTimeoutOverrideStr (macOS) and systemctlTimeoutOverrideStr (Linux)
#   overridden to 5s, and hostCommandTimeoutOverrideStr (Windows) overridden to
#   5s, so a wedged launchctl/systemctl/sc/dsregcmd/WMI call (sc-108855) aborts
#   in seconds instead of the production 5 minutes (service commands) or 30
#   seconds (host-info commands). Each symbol only exists in its own platform's
#   build; -X against a missing symbol is ignored, so all three flags are
#   harmless on the other two platforms.
# - spoolAttemptIntervalOverrideStr overridden to 1s, so a postback result the
#   stand-in engine keeps rejecting exhausts its attempt budget and is
#   abandoned within a few connection cycles instead of (attempts-1) x 10
#   minutes; journalMaxPendingOverrideStr overridden to 20, so the command
#   journal fills with a couple of dozen queued commands rather than a
#   thousand; clockOffsetOverrideFileStr overridden to clock_offset_override,
#   so the hermetic suite can start the agent with the journal's clock stepped
#   forward or backward by writing that file into the data directory
#   (sc-126322). Released builds leave all three empty.

$env:GOARCH = "amd64"

$versionFlag = "-X github.com/RewstApp/agent-smith-go/internal/version.Version=v0.0.0-it"
$intervalFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.updateIntervalStr=30s"
$baseBackoffFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.baseBackoffStr=2s"
$maxRetriesFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.maxRetriesStr=6"
$sasTokenLifetimeFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.sasTokenLifetimeOverrideStr=90s"
$stopTimeoutFlag = "-X github.com/RewstApp/agent-smith-go/internal/service.stopTimeoutOverrideStr=25s"
$exitTimeoutFlag = "-X main.exitTimeoutOverrideStr=25s"
$releaseUrlOverrideFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.releaseUrlOverrideFileStr=release_url_override"
$defaultCommandTimeoutFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.defaultCommandTimeoutOverrideStr=5s"
$launchctlTimeoutFlag = "-X github.com/RewstApp/agent-smith-go/internal/service.launchctlTimeoutOverrideStr=5s"
$systemctlTimeoutFlag = "-X github.com/RewstApp/agent-smith-go/internal/service.systemctlTimeoutOverrideStr=5s"
$hostCommandTimeoutFlag = "-X github.com/RewstApp/agent-smith-go/internal/agent.hostCommandTimeoutOverrideStr=5s"
$spoolAttemptIntervalFlag = "-X main.spoolAttemptIntervalOverrideStr=1s"
$journalMaxPendingFlag = "-X main.journalMaxPendingOverrideStr=20"
$clockOffsetOverrideFlag = "-X main.clockOffsetOverrideFileStr=clock_offset_override"
$ldflags = "-w -s $versionFlag $intervalFlag $baseBackoffFlag $maxRetriesFlag $sasTokenLifetimeFlag $stopTimeoutFlag $exitTimeoutFlag $releaseUrlOverrideFlag $defaultCommandTimeoutFlag $launchctlTimeoutFlag $systemctlTimeoutFlag $hostCommandTimeoutFlag $spoolAttemptIntervalFlag $journalMaxPendingFlag $clockOffsetOverrideFlag"

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
