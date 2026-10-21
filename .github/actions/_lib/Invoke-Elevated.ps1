# Dot-source from a composite action's single pwsh step:
#   . "$env:GITHUB_WORKSPACE/.github/actions/_lib/Invoke-Elevated.ps1"
#   Invoke-Elevated -Script "$env:GITHUB_ACTION_PATH/assert.ps1" -Arguments @{ LogFile = $env:LOG_FILE }
#
# Runs a PowerShell script as the privileged user on every platform: directly
# on Windows (the runner is already an administrator), under sudo on Linux and
# macOS (the agent's log, config and scripts directories are root-owned). The
# environment is preserved whole (sudo -E; the runner's unrestricted sudoers
# permits it) so GITHUB_OUTPUT and the step's variables reach the script.
# On Windows the hashtable is splatted so every entry binds by parameter
# name (an array splat would pass "-Name" as a positional value); under sudo
# the pairs go on the command line as "-Name value", and the called script
# binds them to its typed parameters. A null value is omitted, which is how an
# optional parameter is left at its default. A non-zero exit from the script
# becomes this step's exit code.
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
  $bound = @{}
  $argv = @()
  foreach ($key in $Arguments.Keys) {
    $value = $Arguments[$key]
    if ($null -eq $value) { continue }
    $bound[$key] = $value
    $argv += "-$key"
    $argv += "$value"
  }
  $global:LASTEXITCODE = 0
  if ($IsWindows) {
    & $Script @bound
  } else {
    $pwsh = (Get-Command pwsh).Source
    sudo -E $pwsh -NoProfile -File $Script @argv
  }
  if ($LASTEXITCODE) { exit $LASTEXITCODE }
}
