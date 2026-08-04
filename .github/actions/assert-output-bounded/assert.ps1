#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Asserts that a verbose command's output was bounded rather than buffered whole
(sc-106105).

.DESCRIPTION
Run immediately after dispatching a command that writes far more output than the
configured max_output_bytes ceiling. While waiting for the agent to report the
truncation, the agent process's resident set size is sampled every interval so
the run captures the property the fix exists for: peak RSS tracks the ceiling,
not the volume the command wrote.

Four things are asserted:

  1. The agent process survives, keeping the same pid throughout. A process that
     vanishes or is replaced mid-command is exactly what the OOM kill this fix
     prevents looks like, so either is a failure rather than a retry.
  2. Peak RSS stays under -MaxRssMb. Before the fix the agent's heap tracked the
     command's output at roughly 3-4x, so a command writing hundreds of MB moved
     RSS by hundreds of MB; afterwards it is a small multiple of the ceiling.
  3. Exactly one truncation warning is emitted for the command - the report is
     per command, never per write - and it carries the message id, the ceiling in
     effect, and both byte counts.
  4. The reported counts are consistent with a bounded capture: the command
     produced at least -MinProducedBytes, and what was kept falls in the
     [-MinKeptBytes, -MaxKeptBytes] window the caller expects.

The kept-bytes window is what distinguishes the two flood shapes. A stdout flood
starts at the ceiling, because stdout fills it on its own. A stderr flood starts
just *above* the ceiling, because the small stdout line the script also writes is
kept on top of a full stderr capture - which can only happen if the two streams
are bounded independently instead of sharing one budget.

Callers give the upper end of that window several KiB of headroom rather than
pinning it exactly. The shell contributes output of its own that the scenario does
not control: on Windows this run deliberately injects a PROFILE_NOISE_<guid>
marker into the all-users PowerShell profile, and every agent-spawned PowerShell
echoes it. What the upper bound exists to catch is a stream that was not capped at
all, which lands near the produced volume rather than a few bytes past the
ceiling, so the headroom costs nothing.

Counts are compared against a caller-supplied baseline because the agent log
accumulates across scenarios and already carries warnings from earlier commands.
#>

param(
    [Parameter(Mandatory)][string]$Service,
    [Parameter(Mandatory)][string]$LogFile,
    [Parameter(Mandatory)][int]$BaselineTruncations,
    [Parameter(Mandatory)][int]$ExpectedCeilingBytes,
    [Parameter(Mandatory)][int]$MinKeptBytes,
    [Parameter(Mandatory)][int]$MaxKeptBytes,
    [Parameter(Mandatory)][long]$MinProducedBytes,
    [Parameter(Mandatory)][int]$MaxRssMb,
    [int]$MaxAttempts = 120,
    [int]$IntervalSeconds = 1,
    [int]$SettleSamples = 10
)

$ErrorActionPreference = 'Stop'

$truncationPattern = 'Command output truncated'

# Resolve the running agent from the platform service manager. The pid is
# re-resolved on every sample so a service the manager restarted underneath us
# (Restart=always on Linux, KeepAlive on macOS) is detected as a restart rather
# than silently sampled as a different process.
function Get-AgentPid {
    if ($IsWindows) {
        $query = sc.exe queryex $Service 2>$null
        $match = $query | Select-String -Pattern 'PID\s*:\s*(\d+)'
        if (-not $match) { return 0 }
        return [int]$match.Matches[0].Groups[1].Value
    }

    if ($IsLinux) {
        $mainPid = (systemctl show -p MainPID --value $Service 2>$null | Select-Object -First 1)
        if (-not $mainPid) { return 0 }
        return [int]($mainPid.Trim())
    }

    # macOS: launchctl reports the pid of the running job.
    $print = sudo launchctl print "system/$Service" 2>$null
    $match = $print | Select-String -Pattern '^\s*pid = (\d+)'
    if (-not $match) { return 0 }
    return [int]$match.Matches[0].Groups[1].Value
}

# Resident set size in MB, or -1 when the process is gone.
function Get-RssMb([int]$ProcessId) {
    if ($ProcessId -le 0) { return -1 }

    if ($IsWindows) {
        $proc = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
        if (-not $proc) { return -1 }
        return [math]::Round($proc.WorkingSet64 / 1MB, 1)
    }

    # ps reports RSS in KiB on both Linux and macOS.
    $rssKb = (ps -o rss= -p $ProcessId 2>$null | Select-Object -First 1)
    if (-not $rssKb -or -not $rssKb.Trim()) { return -1 }
    return [math]::Round(([double]$rssKb.Trim()) / 1024, 1)
}

function Get-TruncationCount {
    $content = Get-Content $LogFile -Raw -ErrorAction SilentlyContinue
    if (-not $content) { return 0 }
    return ([regex]::Matches($content, [regex]::Escape($truncationPattern))).Count
}

$agentPid = Get-AgentPid
if ($agentPid -le 0) {
    Write-Error "Could not resolve a running pid for service $Service" -ErrorAction Continue
    exit 1
}

$startRss = Get-RssMb $agentPid
Write-Output "Sampling agent RSS (service $Service, pid $agentPid, start ${startRss} MB, ceiling $ExpectedCeilingBytes bytes)"

$peakRss = $startRss
$samples = [System.Collections.Generic.List[string]]::new()
$samples.Add("0`t$startRss")

$found = $false
$settle = 0

for ($i = 1; $i -le $MaxAttempts; $i++) {
    Start-Sleep -Seconds $IntervalSeconds

    # A changed pid means the service was restarted mid-command; a missing one
    # means the process is simply gone. Both are the failure this fix prevents.
    $currentPid = Get-AgentPid
    if ($currentPid -ne $agentPid) {
        Write-Output ($samples -join "`n")
        Write-Error "Agent process changed mid-command (pid $agentPid -> $currentPid): the service died and was restarted, which is what an OOM kill looks like" -ErrorAction Continue
        exit 1
    }

    $rss = Get-RssMb $agentPid
    if ($rss -lt 0) {
        Write-Output ($samples -join "`n")
        Write-Error "Agent process $agentPid disappeared mid-command (OOM kill?)" -ErrorAction Continue
        exit 1
    }

    $samples.Add("$($i * $IntervalSeconds)`t$rss")
    if ($rss -gt $peakRss) { $peakRss = $rss }

    if (-not $found) {
        if ((Get-TruncationCount) -gt $BaselineTruncations) {
            $found = $true
            Write-Output "Truncation reported after ~$($i * $IntervalSeconds)s (RSS ${rss} MB, peak ${peakRss} MB)"
        }
    }

    if ($found) {
        # Keep sampling briefly past the warning: it is logged as soon as the
        # command exits, but the kept bytes are copied into a string and
        # marshalled into the result afterwards, so peak RSS lands slightly
        # later. Stopping at the warning would under-report the peak.
        $settle++
        if ($settle -ge $SettleSamples) { break }
    }
}

$rssTrace = "elapsed_s`trss_mb`n" + ($samples -join "`n")
Write-Output "RSS samples:`n$rssTrace"

if ($env:GITHUB_STEP_SUMMARY) {
    @(
        "### Agent RSS while a verbose command ran ($Service)",
        "",
        "- ceiling (max_output_bytes): $ExpectedCeilingBytes bytes",
        "- start RSS: $startRss MB",
        "- peak RSS: $peakRss MB (allowed: $MaxRssMb MB)",
        "",
        '```',
        $rssTrace,
        '```',
        ""
    ) | Out-File -FilePath $env:GITHUB_STEP_SUMMARY -Append -Encoding utf8
}

$failed = $false

if (-not $found) {
    Write-Error "Agent never reported '$truncationPattern' (count stayed at $BaselineTruncations) within ~$($MaxAttempts * $IntervalSeconds)s" -ErrorAction Continue
    exit 1
}

# The bound is on peak memory, not on the command: this is the assertion that
# fails loudly if the output is ever buffered whole again.
if ($peakRss -gt $MaxRssMb) {
    Write-Error "Peak agent RSS ${peakRss} MB exceeded the ${MaxRssMb} MB ceiling: command output is not bounded (start was ${startRss} MB)" -ErrorAction Continue
    $failed = $true
} else {
    Write-Output "OK: peak RSS ${peakRss} MB stayed under the ${MaxRssMb} MB ceiling (start ${startRss} MB)"
}

$finalCount = Get-TruncationCount
$delta = $finalCount - $BaselineTruncations
if ($delta -ne 1) {
    Write-Error "Expected exactly one '$truncationPattern' warning for this command, got $delta (baseline $BaselineTruncations, final $finalCount) - is truncation being logged per write?" -ErrorAction Continue
    $failed = $true
} else {
    Write-Output "OK: exactly one truncation warning for this command"
}

$content = Get-Content $LogFile -Raw
$lines = [regex]::Matches(
    $content,
    '(?<line>[^\r\n]*Command output truncated[^\r\n]*)'
)
$line = $lines[$lines.Count - 1].Groups['line'].Value
Write-Output "Truncation log line: $line"

if ($line -notmatch '\[WARN\]') {
    Write-Error "Truncation was not logged at Warn level: $line" -ErrorAction Continue
    $failed = $true
} else {
    Write-Output "OK: truncation logged at Warn level"
}

$fields = [regex]::Match(
    $line,
    'message_id=(?<mid>\S+)\s+max_output_bytes=(?<ceiling>\d+)\s+output_bytes_produced=(?<produced>\d+)\s+output_bytes_kept=(?<kept>\d+)'
)
if (-not $fields.Success) {
    Write-Error "Truncation log line is missing the message_id / max_output_bytes / output_bytes_produced / output_bytes_kept fields: $line" -ErrorAction Continue
    exit 1
}

$messageId = $fields.Groups['mid'].Value.Trim('"')
$ceiling = [long]$fields.Groups['ceiling'].Value
$produced = [long]$fields.Groups['produced'].Value
$kept = [long]$fields.Groups['kept'].Value
Write-Output "Parsed truncation fields -> message_id=$messageId max_output_bytes=$ceiling produced=$produced kept=$kept"

if (-not $messageId) {
    Write-Error "Truncation log line carried an empty message_id: $line" -ErrorAction Continue
    $failed = $true
}

if ($ceiling -ne $ExpectedCeilingBytes) {
    Write-Error "Truncation reported max_output_bytes=$ceiling, expected the configured ${ExpectedCeilingBytes}: the per-device override was not honored" -ErrorAction Continue
    $failed = $true
}

if ($produced -lt $MinProducedBytes) {
    Write-Error "Truncation reported output_bytes_produced=$produced, expected at least ${MinProducedBytes}: the command did not write the volume this scenario needs" -ErrorAction Continue
    $failed = $true
}

if ($kept -lt $MinKeptBytes -or $kept -gt $MaxKeptBytes) {
    Write-Error "Truncation reported output_bytes_kept=$kept, expected between $MinKeptBytes and $MaxKeptBytes" -ErrorAction Continue
    $failed = $true
}

if ($kept -ge $produced) {
    Write-Error "Truncation reported kept=$kept >= produced=$produced, so nothing was actually discarded" -ErrorAction Continue
    $failed = $true
}

if ($failed) { exit 1 }

Write-Output "Confirmed bounded output: produced $produced bytes, kept $kept, peak RSS $peakRss MB (ceiling $ExpectedCeilingBytes bytes)"

# Explicit success: this script shells out to ps / sc.exe / launchctl while
# sampling, so returning implicitly would let one of those exit codes stand in
# for the assertions' verdict.
exit 0
