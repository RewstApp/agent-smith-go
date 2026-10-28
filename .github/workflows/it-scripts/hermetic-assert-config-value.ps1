# Assert a key of the agent's config file (sc-129688: a runtime setting the
# device twin applied is written to the file so a restart keeps it). The config
# file sits next to the log file in the agent's data directory.
# Env: LOG_FILE, KEY, EXPECTED (the value as text: "True"/"False", a number or
# a string).
$path = Join-Path (Split-Path $env:LOG_FILE -Parent) 'config.json'
$config = Get-Content $path -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop
$actual = $config.($env:KEY)
if ("$actual" -ne $env:EXPECTED) {
  Write-Error "config file $path has $($env:KEY) = '$actual', expected '$($env:EXPECTED)'"
  exit 1
}
Write-Output "config file $path has $($env:KEY) = $actual"
