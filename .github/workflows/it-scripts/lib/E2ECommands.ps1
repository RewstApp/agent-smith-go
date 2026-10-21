# Dot-source from an it-script to resolve a scenario's command from the
# catalog: `. "$PSScriptRoot/lib/E2ECommands.ps1"; $cmd = Get-E2ECommand sweep`.
# The catalog (.github/workflows/e2e-commands.json) holds each scenario's
# command per platform family so neither the workflow nor the matrix carries
# per-platform command text (sc-126318). E2E_CATALOG overrides the path, which
# is how the Go tests point the scripts at a fixture.
function Get-E2ECommand {
  param(
    [Parameter(Mandatory)][string]$Scenario,
    [string]$Catalog = ''
  )
  if ($env:E2E_CATALOG) { $Catalog = $env:E2E_CATALOG }
  if (-not $Catalog) { $Catalog = Join-Path $env:GITHUB_WORKSPACE '.github/workflows/e2e-commands.json' }
  $family = if ($IsWindows) { 'windows' } else { 'unix' }
  $entry = (Get-Content -LiteralPath $Catalog -Raw | ConvertFrom-Json).$Scenario
  if (-not $entry) { throw "unknown e2e scenario '$Scenario' (catalog $Catalog)" }
  $command = $entry.$family
  if (-not $command) { throw "e2e scenario '$Scenario' has no '$family' command (catalog $Catalog)" }
  return $command
}
