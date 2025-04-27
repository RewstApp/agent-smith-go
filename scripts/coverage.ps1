#Requires -Version 7
param(
    [double]$MinCoverage = 80.0    # Default minimum coverage is 80.0%
)

# Generate the coverage profile
go test -coverprofile ./dist/coverage.out ./...
if ($LASTEXITCODE -ne 0) {
    exit 1
}

# Extract the total coverage
$coverageOutput = go tool cover -func ./dist/coverage.out
$totalLine = $coverageOutput | Select-String "total:"
$coverageString = ($totalLine -split '\s+')[-1]

# Remove the % sign and convert to number
$coverage = [double]($coverageString.TrimEnd('%'))

# Check if below minimum coverage
if ($coverage -lt $MinCoverage) {
    Write-Host "Coverage $coverage% is below threshold $MinCoverage%."
    exit 1
}
else {
    Write-Host "Coverage $coverage% meets the threshold."
}
