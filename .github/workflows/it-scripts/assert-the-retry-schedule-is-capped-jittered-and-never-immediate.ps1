#!/usr/bin/env pwsh
#Requires -Version 7

# The cap the integration build lands on: a quarter of its 30s check
# interval, which is below the absolute 1h ceiling a released build uses.
$cap = 7.5
$baseline = [int]$env:BASELINE
$failures = @()

function ConvertTo-Seconds([string]$value) {
  # Go renders a duration as concatenated unit components ("4.1s",
  # "1m4s"), and a negative one with a leading '-'. The sign is read
  # separately because the overflow this scenario guards against
  # produced exactly that negative value, and summing the components
  # alone would report it as positive.
  $sign = if ($value.StartsWith('-')) { -1.0 } else { 1.0 }
  $total = 0.0
  foreach ($m in [regex]::Matches($value, '(?<n>[0-9.]+)(?<u>ms|us|ns|h|m|s)')) {
    $n = [double]$m.Groups['n'].Value
    switch ($m.Groups['u'].Value) {
      'ns' { $total += $n / 1e9 }
      'us' { $total += $n / 1e6 }
      'ms' { $total += $n / 1e3 }
      's'  { $total += $n }
      'm'  { $total += $n * 60 }
      'h'  { $total += $n * 3600 }
    }
  }
  return $sign * $total
}

$lines = @(Get-Content $env:LOG_FILE -ErrorAction SilentlyContinue |
  Where-Object { $_ -match 'Retrying update' } |
  Select-Object -Skip $baseline)

$slots = @()
foreach ($line in $lines) {
  # hclog writes "<timestamp> [INFO]  agent_smith: Retrying update: attempt=N backoff=D".
  # The timestamp is a fixed-width local time followed by its offset, so
  # the first 23 characters parse without worrying about the offset form.
  $m = [regex]::Match($line, '^(?<ts>\S{23}).*Retrying update: attempt=(?<attempt>\d+) backoff=(?<backoff>\S+)')
  if (-not $m.Success) {
    $failures += "could not parse a retry line: $line"
    continue
  }
  $stamp = $null
  try {
    $stamp = [datetime]::ParseExact(
      $m.Groups['ts'].Value,
      'yyyy-MM-ddTHH:mm:ss.fff',
      [Globalization.CultureInfo]::InvariantCulture)
  } catch {
    $failures += "could not parse the timestamp of: $line"
  }
  $slots += [pscustomobject]@{
    Attempt = [int]$m.Groups['attempt'].Value
    Backoff = ConvertTo-Seconds $m.Groups['backoff'].Value
    Time    = $stamp
  }
}

if ($slots.Count -lt 3) {
  Write-Error "Expected at least three retry slots to inspect, got $($slots.Count)"
  exit 1
}

# The timestamp table the ticket asks for: every slot, what it waited,
# and how long actually elapsed before the next one arrived.
Write-Output "---- observed update retry schedule ----"
for ($i = 0; $i -lt $slots.Count; $i++) {
  $gap = ''
  if ($i -lt $slots.Count - 1 -and $slots[$i].Time -and $slots[$i + 1].Time) {
    $gap = [math]::Round(($slots[$i + 1].Time - $slots[$i].Time).TotalSeconds, 3)
  }
  Write-Output ("attempt={0} backoff={1}s elapsed_to_next={2}" -f $slots[$i].Attempt, [math]::Round($slots[$i].Backoff, 3), $gap)
}

foreach ($slot in $slots) {
  # A zero or negative slot is the overflow busy-spin: time.After fires
  # immediately and the loop issues requests as fast as it can.
  if ($slot.Backoff -le 0) {
    $failures += "attempt $($slot.Attempt): backoff $($slot.Backoff)s is not strictly positive"
  }
  # The tolerance covers the millisecond rendering, not the cap itself.
  if ($slot.Backoff -gt ($cap + 0.05)) {
    $failures += "attempt $($slot.Attempt): backoff $($slot.Backoff)s exceeds the ${cap}s cap"
  }
}

# Jitter: a fixed schedule repeats the same values bit for bit.
$distinct = @($slots | ForEach-Object { [math]::Round($_.Backoff, 3) } | Select-Object -Unique)
if ($distinct.Count -lt 2) {
  $failures += "every retry slot was $($distinct -join ', ')s - the schedule is not jittered"
}
# Distinct values alone are weak evidence, because the doubling produces
# distinct values on its own until it reaches the ceiling. The slots that
# do reach it are the discriminator: an unjittered schedule pins every
# one of them to exactly the cap, while jitter spreads them across the
# band below it. Skipped when the sequence was cut short by the recovery
# flip before two slots got that far.
$nearCap = @($slots | Where-Object { $_.Backoff -ge ($cap * 0.75) })
if ($nearCap.Count -ge 2) {
  $distinctNearCap = @($nearCap | ForEach-Object { [math]::Round($_.Backoff, 3) } | Select-Object -Unique)
  if ($distinctNearCap.Count -lt 2) {
    $failures += "the capped slots all measured $($distinctNearCap -join ', ')s - the jitter is not being applied"
  }
}

# Growth: the schedule still doubles up to the cap rather than being flat.
$maxBackoff = ($slots | Measure-Object -Property Backoff -Maximum).Maximum
if ($maxBackoff -le $slots[0].Backoff) {
  $failures += "the schedule never grew (first $($slots[0].Backoff)s, largest ${maxBackoff}s)"
}

# Spacing: consecutive slots of one sequence must be separated by at
# least the wait that was logged. Pairs that are not consecutive attempts
# belong to different retry sequences and are skipped.
for ($i = 0; $i -lt $slots.Count - 1; $i++) {
  if ($slots[$i + 1].Attempt -ne $slots[$i].Attempt + 1) { continue }
  if (-not $slots[$i].Time -or -not $slots[$i + 1].Time) { continue }
  $gap = ($slots[$i + 1].Time - $slots[$i].Time).TotalSeconds
  if ($gap -lt 1) {
    $failures += "attempts $($slots[$i].Attempt) and $($slots[$i + 1].Attempt) arrived ${gap}s apart - back to back"
  }
  if ($gap -lt ($slots[$i].Backoff * 0.8)) {
    $failures += "attempt $($slots[$i].Attempt) logged a $($slots[$i].Backoff)s backoff but the next retry came after ${gap}s"
  }
}

if ($failures.Count -gt 0) {
  $failures | ForEach-Object { Write-Output "FAIL: $_" }
  Write-Error "the update retry schedule is not capped, jittered and spaced as expected"
  exit 1
}
Write-Output "Retry schedule bounded by ${cap}s, jittered across $($distinct.Count) distinct values, and never served back to back"
exit 0

