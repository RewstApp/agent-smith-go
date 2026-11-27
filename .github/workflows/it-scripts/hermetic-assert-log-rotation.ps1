# Assert the agent's rotated log copies are compressed and bounded
# (sc-132996, log_compress): poll until at least MIN_COMPRESSED <log>.N.gz
# copies exist beside the log and no plain <log>.N is left (the agent gzips
# each copy just after it rotates it), then check every compressed copy is
# valid gzip holding the agent's own log lines, no slot is numbered past
# MAX_FILES, and no compression temp file is left behind.
# Env: LOG_FILE, MAX_FILES, MIN_COMPRESSED, TIMEOUT_SECONDS.
$ErrorActionPreference = 'Stop'
if (-not $env:LOG_FILE) { Write-Error "LOG_FILE is not set"; exit 1 }
foreach ($name in 'MAX_FILES', 'MIN_COMPRESSED', 'TIMEOUT_SECONDS') {
  $value = [Environment]::GetEnvironmentVariable($name)
  if ($value -notmatch '^[1-9][0-9]*$') {
    Write-Error "$name must be a positive integer, got '$value'"
    exit 1
  }
}
$maxFiles = [int]$env:MAX_FILES
$minCompressed = [int]$env:MIN_COMPRESSED
$dir = Split-Path $env:LOG_FILE -Parent
$base = Split-Path $env:LOG_FILE -Leaf
$pattern = '^' + [regex]::Escape($base) + '\.([0-9]+)(\.gz)?$'

function Get-RotatedCopies {
  $found = [System.Collections.Generic.List[object]]::new()
  foreach ($f in Get-ChildItem -File -LiteralPath $dir) {
    if ($f.Name -match $pattern) {
      $found.Add([pscustomobject]@{
          Name       = $f.Name
          Path       = $f.FullName
          Slot       = [int]$Matches[1]
          Compressed = ($Matches[2] -eq '.gz')
        })
    }
  }
  return $found
}

$deadline = (Get-Date).AddSeconds([int]$env:TIMEOUT_SECONDS)
while ($true) {
  $copies = @(Get-RotatedCopies)
  $compressed = @($copies | Where-Object { $_.Compressed })
  $plain = @($copies | Where-Object { -not $_.Compressed })
  if (($compressed.Count -ge $minCompressed -and $plain.Count -eq 0) -or (Get-Date) -ge $deadline) { break }
  Start-Sleep -Seconds 2
}
Write-Output "beside ${base}: $(@($copies | ForEach-Object { $_.Name }) -join ', ')"
if ($compressed.Count -lt $minCompressed) {
  Write-Error "$($compressed.Count) compressed cop(ies) after $($env:TIMEOUT_SECONDS)s, expected at least ${minCompressed}: the log did not rotate enough, or log_compress is off"
  exit 1
}
if ($plain.Count -gt 0) {
  Write-Error "plain rotated copies are left: $(@($plain | ForEach-Object { $_.Name }) -join ', ')"
  exit 1
}
$beyond = @($copies | Where-Object { $_.Slot -gt $maxFiles })
if ($beyond.Count -gt 0) {
  Write-Error "copies past the retention bound of ${maxFiles}: $(@($beyond | ForEach-Object { $_.Name }) -join ', ')"
  exit 1
}
$temps = @(Get-ChildItem -File -LiteralPath $dir | Where-Object { $_.Name.StartsWith("$base.") -and $_.Name.EndsWith('.gz.tmp') })
if ($temps.Count -gt 0) {
  Write-Error "compression temp files are left: $(@($temps | ForEach-Object { $_.Name }) -join ', ')"
  exit 1
}
foreach ($copy in $compressed) {
  # Shared for delete too, so a rotation the agent makes meanwhile is never
  # blocked by this read on Windows.
  $stream = [IO.File]::Open($copy.Path, 'Open', 'Read', 'ReadWrite, Delete')
  try {
    $gzip = [IO.Compression.GZipStream]::new($stream, [IO.Compression.CompressionMode]::Decompress)
    $text = [IO.StreamReader]::new($gzip).ReadToEnd()
  } catch {
    Write-Error "$($copy.Name) is not valid gzip: $($_.Exception.Message)"
    exit 1
  } finally {
    $stream.Dispose()
  }
  if (-not $text.Contains('agent_smith')) {
    Write-Error "$($copy.Name) does not hold the agent's log lines"
    exit 1
  }
  Write-Output "$($copy.Name): $((Get-Item -LiteralPath $copy.Path).Length) bytes holding $($text.Length) characters of log"
}
Write-Output "$($compressed.Count) rotated cop(ies), every one compressed, none past slot $maxFiles"
