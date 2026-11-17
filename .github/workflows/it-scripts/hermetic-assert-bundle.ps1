# Write the agent's diagnostic bundle (sc-129690) and check it: the expected
# entries are present, config.json is redacted, and neither the shared access
# key (read from the agent's own config file, next to its log) nor any ABSENT
# value appears anywhere in it. Then the hand-over (sc-132995): this helper
# runs under sudo on Linux and macOS, so SUDO_UID names the runner user and
# the file must end up theirs; on Windows the current user must hold a read
# grant. Then `--bundle-path -` must put a zip with the same entries on
# stdout and the console output on stderr, and `--bundle-path <directory>`
# must write the default name inside it. Then ask for a bundle of an org that
# is not installed: it must fail with a clear message and leave no file.
# Env: AGENT_BINARY, ORG_ID, LOG_FILE, BUNDLE_PATH, ABSENT (newline-separated).
#
# Two modes check one guarantee each on the file form alone (sc-133003):
#   EXPECT_WITHHELD=<line>: a command is running and <line> is its last output
#     line. It must appear in no entry, and every in-flight entry in the
#     bundle's health.json must carry "(withheld from the bundle)" in its place.
#   CORRUPT_CONFIG_FIELD=<field>: <field> is set to a string in the installed
#     config.json for the run (restored byte for byte afterwards, whatever
#     happens), and diagnostic.txt must open with a Configuration section that
#     names it, before the Service Status section.
$ErrorActionPreference = 'Stop'
$configPath = Join-Path (Split-Path $env:LOG_FILE -Parent) 'config.json'
$config = Get-Content $configPath -Raw | ConvertFrom-Json
$secrets = @($config.shared_access_key) + @(($env:ABSENT -split "`r?`n") | Where-Object { $_ })
$secrets = @($secrets | Where-Object { $_ })
if ($secrets.Count -lt 1) { Write-Error "no secret to look for"; exit 1 }

$required = @('config.json', 'diagnostic.txt', 'host.txt', 'journal.txt', 'service.txt', 'spool.txt', 'version.txt')

# Assert-Bundle expands the zip at $path and checks its entries and redaction.
function Assert-Bundle([string]$path, [string]$label) {
  $dir = Join-Path ([IO.Path]::GetTempPath()) ("bundle-" + [guid]::NewGuid())
  Expand-Archive -Path $path -DestinationPath $dir
  $files = Get-ChildItem -Recurse -File $dir
  $names = @($files | ForEach-Object { $_.FullName.Substring($dir.Length + 1).Replace('\', '/') })
  Write-Output "${label}: $($names -join ', ')"
  foreach ($name in $required) {
    if ($names -notcontains $name) { Write-Error "the $label lacks $name"; exit 1 }
  }
  if (-not ($names | Where-Object { $_ -like 'logs/*' })) { Write-Error "the $label has no log tail"; exit 1 }
  $bundled = Get-Content (Join-Path $dir 'config.json') -Raw | ConvertFrom-Json
  if ($bundled.shared_access_key -ne '***') { Write-Error "the $label's config.json shared_access_key is not redacted"; exit 1 }
  foreach ($file in $files) {
    $text = [IO.File]::ReadAllText($file.FullName)
    foreach ($secret in $secrets) {
      if ($text.Contains($secret)) { Write-Error "$($file.Name) in the $label contains a secret"; exit 1 }
    }
    if ($env:EXPECT_WITHHELD -and $text.Contains($env:EXPECT_WITHHELD)) {
      Write-Error "$($file.Name) in the $label carries the running command's output line"; exit 1
    }
  }
  if ($env:EXPECT_WITHHELD) {
    $healthPath = Join-Path $dir 'health.json'
    if (-not (Test-Path $healthPath)) { Write-Error "the $label has no health.json"; exit 1 }
    $healthText = [IO.File]::ReadAllText($healthPath)
    $inFlight = @(($healthText | ConvertFrom-Json).commands.in_flight)
    if ($inFlight.Count -lt 1) { Write-Output $healthText; Write-Error "the $label's health.json lists no running command"; exit 1 }
    $withheld = @($inFlight | Where-Object { $_.last_output_line -eq '(withheld from the bundle)' })
    if ($withheld.Count -ne $inFlight.Count) {
      Write-Output $healthText; Write-Error "the $label's health.json does not withhold every running command's last output line"; exit 1
    }
    Write-Output "the $label withholds the running command's output line: $($inFlight.Count) in-flight entr(ies) carry the marker"
  }
  if ($env:CORRUPT_CONFIG_FIELD) {
    $diag = [IO.File]::ReadAllText((Join-Path $dir 'diagnostic.txt'))
    $section = $diag.IndexOf('Configuration')
    $service = $diag.IndexOf('Service Status')
    if ($section -lt 0 -or ($service -ge 0 -and $section -gt $service)) {
      Write-Output $diag; Write-Error "diagnostic.txt in the $label does not open with the Configuration section"; exit 1
    }
    if (-not $diag.Contains('config.json does not load as the agent') -or -not $diag.Contains($env:CORRUPT_CONFIG_FIELD)) {
      Write-Output $diag; Write-Error "diagnostic.txt in the $label does not report $($env:CORRUPT_CONFIG_FIELD) as the field that does not load"; exit 1
    }
    Write-Output "the $label's diagnostic.txt opens with the Configuration section naming $($env:CORRUPT_CONFIG_FIELD)"
  }
  Remove-Item -Recurse -Force $dir
}

$original = $null
if ($env:CORRUPT_CONFIG_FIELD) {
  $original = [IO.File]::ReadAllBytes($configPath)
  $doc = Get-Content $configPath -Raw | ConvertFrom-Json
  $doc | Add-Member -NotePropertyName $env:CORRUPT_CONFIG_FIELD -NotePropertyValue 'many' -Force
  Set-Content -Path $configPath -Value ($doc | ConvertTo-Json -Depth 10) -NoNewline
  Write-Output "set $($env:CORRUPT_CONFIG_FIELD) to a string in $configPath for this run"
}
try {
  Remove-Item -Force -ErrorAction SilentlyContinue $env:BUNDLE_PATH
  $global:LASTEXITCODE = 0
  $out = & $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic --bundle --bundle-path $env:BUNDLE_PATH 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) { Write-Output $out; Write-Error "--diagnostic --bundle exited $LASTEXITCODE"; exit 1 }
  if (-not $out.Contains("Diagnostic bundle written: $($env:BUNDLE_PATH)")) {
    Write-Output $out; Write-Error "the bundle path was not printed"; exit 1
  }
  Assert-Bundle $env:BUNDLE_PATH 'bundle'
  Write-Output "the bundle carries every entry and none of $($secrets.Count) secret value(s)"
} finally {
  if ($null -ne $original) {
    [IO.File]::WriteAllBytes($configPath, $original)
    Write-Output "restored $configPath"
  }
}
if ($env:EXPECT_WITHHELD -or $env:CORRUPT_CONFIG_FIELD) { exit 0 }

# The hand-over (sc-132995).
$written = ($out -split "`r?`n" | Where-Object { $_ -like 'Diagnostic bundle written:*' }) -join ''
if ($IsWindows) {
  $grants = (Get-Acl $env:BUNDLE_PATH).Access | Where-Object {
    $_.IdentityReference.Value -like "*\$($env:USERNAME)" -and
    $_.AccessControlType -eq 'Allow' -and
    ($_.FileSystemRights -band [Security.AccessControl.FileSystemRights]::Read)
  }
  if (-not $grants) { icacls $env:BUNDLE_PATH; Write-Output $written; Write-Error "$env:USERNAME holds no read grant on the bundle"; exit 1 }
  if (-not $written.Contains("read granted to ")) { Write-Error "the closing line does not name the grant: $written"; exit 1 }
  Write-Output "the bundle grants $env:USERNAME read: $written"
} else {
  if (-not $env:SUDO_UID) { Write-Error "this helper expects to run under sudo (SUDO_UID is unset)"; exit 1 }
  $owner = (ls -ln $env:BUNDLE_PATH) -split '\s+' | Select-Object -Index 2
  if ("$owner" -ne "$($env:SUDO_UID)") { Write-Output $written; Write-Error "the bundle is owned by uid $owner, not the sudo user $($env:SUDO_UID)"; exit 1 }
  if (-not $written.Contains("owned by uid $($env:SUDO_UID) ")) { Write-Error "the closing line does not name the owner: $written"; exit 1 }
  Write-Output "the bundle is owned by the sudo user (uid $owner): $written"
}

# --bundle-path - : the zip on stdout, the console output on stderr. The
# process is started directly and its stdout stream copied to the file byte
# for byte: PowerShell's own redirections (Start-Process, `>`) decode a native
# command's output as text, which corrupts a zip.
$stdoutZip = "$($env:BUNDLE_PATH).stdout.zip"
Remove-Item -Force -ErrorAction SilentlyContinue $stdoutZip
$psi = [System.Diagnostics.ProcessStartInfo]::new($env:AGENT_BINARY)
foreach ($arg in @('--org-id', $env:ORG_ID, '--diagnostic', '--bundle', '--bundle-path', '-')) { $psi.ArgumentList.Add($arg) }
$psi.UseShellExecute = $false
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$proc = [System.Diagnostics.Process]::Start($psi)
$errTask = $proc.StandardError.ReadToEndAsync()
$zipStream = [IO.File]::Create($stdoutZip)
$proc.StandardOutput.BaseStream.CopyTo($zipStream)
$zipStream.Dispose()
$proc.WaitForExit()
$err = $errTask.GetAwaiter().GetResult()
if ($proc.ExitCode -ne 0) { Write-Output $err; Write-Error "--bundle-path - exited $($proc.ExitCode)"; exit 1 }
if (-not $err.Contains('Diagnostic bundle written: stdout (')) { Write-Output $err; Write-Error "stderr does not carry the closing line"; exit 1 }
if (-not $err.Contains('Service Status')) { Write-Output $err; Write-Error "stderr does not carry the console sections"; exit 1 }
Assert-Bundle $stdoutZip 'stdout bundle'
Write-Output "--bundle-path - streams the zip to stdout ($((Get-Item $stdoutZip).Length) bytes) with the sections on stderr"

# --bundle-path <directory> : the default name inside it.
$dirTarget = Join-Path (Split-Path $env:BUNDLE_PATH -Parent) ("bundle-dir-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $dirTarget | Out-Null
$global:LASTEXITCODE = 0
$out = & $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic --bundle --bundle-path $dirTarget 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { Write-Output $out; Write-Error "--bundle-path <directory> exited $LASTEXITCODE"; exit 1 }
$inDir = @(Get-ChildItem -File $dirTarget -Filter "diagnostic-$($env:ORG_ID)-*.zip")
if ($inDir.Count -ne 1) { Write-Output $out; Write-Error "expected one diagnostic-<org>-<timestamp>.zip in $dirTarget, found $($inDir.Count)"; exit 1 }
# Compared in the form the directory was passed in: on Windows the agent prints
# the path it was given, which may be an 8.3 short name where FullName is not.
$expected = Join-Path $dirTarget $inDir[0].Name
if (-not $out.Contains("Diagnostic bundle written: $expected")) { Write-Output $out; Write-Error "the file inside the directory was not printed as $expected"; exit 1 }
Write-Output "--bundle-path <directory> wrote $($inDir[0].Name) inside it"

$missingOrg = [guid]::NewGuid().ToString()
$missingPath = "$($env:BUNDLE_PATH).missing.zip"
$global:LASTEXITCODE = 0
$out = & $env:AGENT_BINARY --org-id $missingOrg --diagnostic --bundle --bundle-path $missingPath 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) { Write-Output $out; Write-Error "a bundle of an org that is not installed succeeded"; exit 1 }
if (-not $out.Contains('nothing to bundle')) { Write-Output $out; Write-Error "the failure is not explained"; exit 1 }
if (Test-Path $missingPath) { Write-Error "a failed bundle left a file behind"; exit 1 }
Write-Output "an org that is not installed fails clearly: $($out.Trim())"
