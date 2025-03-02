# Build the executables for windows
$env:GOARCH = "amd64" # Use 64-bit as default architecture
$env:GOOS = "windows"
$versionFlag = "-X github.com/RewstApp/agent-smith-go/internal/version.Version=v$(cz version -p)"
go build -ldflags="-w -s $versionFlag" -o "./dist/rewst_agent_config.win.exe" "./cmd/agent_smith"

# Create a build winres.json for patch
$winVersion = "$(cz version -p).0"
$winresObj = Get-Content -Path "./winres/winres.json" | Out-String | ConvertFrom-Json
$winresObj."RT_MANIFEST"."#1"."0409"."identity"."version" = $winVersion
$winresObj."RT_VERSION"."#1"."0000"."fixed"."file_version" = $winVersion
$winresObj."RT_VERSION"."#1"."0000"."fixed"."product_version" = $winVersion
$winresObj | ConvertTo-Json -Depth 16 -Compress | Out-File -FilePath "./dist/winres.json" -Encoding ASCII

# Copy the icon
Copy-Item "./winres/logo-rewsty.ico" "./dist/logo-rewsty.ico"

# Use the go-winres to patch the executables
go-winres patch --no-backup --in "./dist/winres.json" "./dist/rewst_agent_config.win.exe"
