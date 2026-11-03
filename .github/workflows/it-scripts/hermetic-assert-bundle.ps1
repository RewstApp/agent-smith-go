# Write the agent's diagnostic bundle (sc-129690) and check it: the expected
# entries are present, config.json is redacted, and neither the shared access
# key (read from the agent's own config file, next to its log) nor any ABSENT
# value appears anywhere in it. Then ask for a bundle of an org that is not
# installed: it must fail with a clear message and leave no file.
# Env: AGENT_BINARY, ORG_ID, LOG_FILE, BUNDLE_PATH, ABSENT (newline-separated).
$ErrorActionPreference = 'Stop'
$config = Get-Content (Join-Path (Split-Path $env:LOG_FILE -Parent) 'config.json') -Raw | ConvertFrom-Json
$secrets = @($config.shared_access_key) + @(($env:ABSENT -split "`r?`n") | Where-Object { $_ })
$secrets = @($secrets | Where-Object { $_ })
if ($secrets.Count -lt 1) { Write-Error "no secret to look for"; exit 1 }

Remove-Item -Force -ErrorAction SilentlyContinue $env:BUNDLE_PATH
$global:LASTEXITCODE = 0
$out = & $env:AGENT_BINARY --org-id $env:ORG_ID --diagnostic --bundle --bundle-path $env:BUNDLE_PATH 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { Write-Output $out; Write-Error "--diagnostic --bundle exited $LASTEXITCODE"; exit 1 }
if (-not $out.Contains("Diagnostic bundle written: $($env:BUNDLE_PATH)")) {
  Write-Output $out; Write-Error "the bundle path was not printed"; exit 1
}

$dir = Join-Path ([IO.Path]::GetTempPath()) ("bundle-" + [guid]::NewGuid())
Expand-Archive -Path $env:BUNDLE_PATH -DestinationPath $dir
$files = Get-ChildItem -Recurse -File $dir
$names = @($files | ForEach-Object { $_.FullName.Substring($dir.Length + 1).Replace('\', '/') })
Write-Output "bundle entries: $($names -join ', ')"
foreach ($required in @('config.json', 'diagnostic.txt', 'host.txt', 'journal.txt', 'service.txt', 'spool.txt', 'version.txt')) {
  if ($names -notcontains $required) { Write-Error "the bundle lacks $required"; exit 1 }
}
if (-not ($names | Where-Object { $_ -like 'logs/*' })) { Write-Error "the bundle has no log tail"; exit 1 }
$bundled = Get-Content (Join-Path $dir 'config.json') -Raw | ConvertFrom-Json
if ($bundled.shared_access_key -ne '***') { Write-Error "config.json's shared_access_key is not redacted"; exit 1 }
foreach ($file in $files) {
  $text = [IO.File]::ReadAllText($file.FullName)
  foreach ($secret in $secrets) {
    if ($text.Contains($secret)) { Write-Error "$($file.Name) contains a secret"; exit 1 }
  }
}
Remove-Item -Recurse -Force $dir
Write-Output "the bundle carries every entry and none of $($secrets.Count) secret value(s)"

$missingOrg = [guid]::NewGuid().ToString()
$missingPath = "$($env:BUNDLE_PATH).missing.zip"
$global:LASTEXITCODE = 0
$out = & $env:AGENT_BINARY --org-id $missingOrg --diagnostic --bundle --bundle-path $missingPath 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) { Write-Output $out; Write-Error "a bundle of an org that is not installed succeeded"; exit 1 }
if (-not $out.Contains('nothing to bundle')) { Write-Output $out; Write-Error "the failure is not explained"; exit 1 }
if (Test-Path $missingPath) { Write-Error "a failed bundle left a file behind"; exit 1 }
Write-Output "an org that is not installed fails clearly: $($out.Trim())"

# The last agent call above fails on purpose, leaving LASTEXITCODE at 1;
# every check passed, so say so explicitly rather than let it leak out.
exit 0
