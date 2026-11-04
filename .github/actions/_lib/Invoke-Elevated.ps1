# Dot-source from a composite action's single pwsh step:
#   . "$env:GITHUB_WORKSPACE/.github/actions/_lib/Invoke-Elevated.ps1"
#   Invoke-Elevated -Script "$env:GITHUB_ACTION_PATH/assert.ps1" -Arguments @{ LogFile = $env:LOG_FILE }
#
# Runs a PowerShell script as the privileged user on every platform, always in
# its own `pwsh -NoProfile -File` process: plain on Windows (the runner is
# already an administrator), under sudo on Linux and macOS (the agent's log,
# config and scripts directories are root-owned). One launch shape on every
# platform is what keeps the exit-code semantics identical: a script that ends
# on a native call that was expected to fail leaves `$LASTEXITCODE` non-zero,
# which `pwsh -File` reports as 0 while an in-process `& script` (how Windows
# used to run it) let leak out as the step's exit code - the bundle assertion
# went red on Windows alone for a reason that was not the agent (sc-129690). The
# environment is forwarded whole and explicitly - every variable of the calling
# step as a NAME=VALUE argument to env(1) between sudo and pwsh - so
# GITHUB_OUTPUT and the step's variables reach the script on every sudo. It
# used to rely on `sudo -E`; on 2026-10-22 ubuntu-latest moved to Ubuntu 26.04,
# whose sudo-rs answers `-E` with "preserving the entire environment is not
# supported, '-E' is ignored" and resets the environment, so every helper on
# ubuntu read its LOG_FILE as null and the gate went red for a reason that was
# not the agent (sc-129246). env(1) applies the assignments itself, so nothing
# here depends on which sudo the image ships or on its env_keep policy.
# The pairs go on the command line as "-Name value", and the called script
# binds them to its typed parameters. A null value is omitted, which is how an
# optional parameter is left at its default. A non-zero exit from the script
# (its `exit N`, or 1 for a terminating error) becomes this step's exit code.
#
# This is the one place the composite actions branch on the platform for the
# helpers whose logic is identical everywhere (sc-126318). Actions whose
# logic genuinely differs per service manager or platform (systemctl versus
# launchctl versus sc.exe, the network disruption, the service-account
# provisioning, the stub launchers) keep their own per-platform steps.
function Invoke-Elevated {
  param(
    [Parameter(Mandatory)][string]$Script,
    [hashtable]$Arguments = @{}
  )
  $argv = @()
  foreach ($key in $Arguments.Keys) {
    $value = $Arguments[$key]
    if ($null -eq $value) { continue }
    $argv += "-$key"
    $argv += "$value"
  }
  $global:LASTEXITCODE = 0
  $pwsh = (Get-Command pwsh).Source
  if ($IsWindows) {
    & $pwsh -NoProfile -File $Script @argv
  } else {
    $environment = Get-ElevatedEnvironment
    sudo env @environment $pwsh -NoProfile -File $Script @argv
  }
  if ($LASTEXITCODE) { exit $LASTEXITCODE }
}

# Get-ElevatedEnvironment returns the calling process's environment as
# NAME=VALUE strings for env(1). Every variable is forwarded, as `sudo -E` did,
# rather than a per-step allow-list that would drift between helpers.
function Get-ElevatedEnvironment {
  $pairs = @()
  foreach ($entry in (Get-ChildItem env:)) {
    $pairs += "$($entry.Name)=$($entry.Value)"
  }
  return $pairs
}
