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
$ErrorActionPreference = 'Stop'
$config = Get-Content (Join-Path (Split-Path $env:LOG_FILE -Parent) 'config.json') -Raw | ConvertFrom-Json
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
  }
  Remove-Item -Recurse -Force $dir
}

Remove-Item -Force -ErrorAction SilentlyContinue $env:BUNDLE_PATH
$global:LASTEXITCODE = 0
$out = & $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic --bundle --bundle-path $env:BUNDLE_PATH 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { Write-Output $out; Write-Error "--diagnostic --bundle exited $LASTEXITCODE"; exit 1 }
if (-not $out.Contains("Diagnostic bundle written: $($env:BUNDLE_PATH)")) {
  Write-Output $out; Write-Error "the bundle path was not printed"; exit 1
}
Assert-Bundle $env:BUNDLE_PATH 'bundle'
Write-Output "the bundle carries every entry and none of $($secrets.Count) secret value(s)"

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
