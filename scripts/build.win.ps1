# Build the executables for windows
$versionFlag = "-X github.com/RewstApp/agent-smith-go/internal/version.Version=v$(cz version -p)"
go build -ldflags="-w -s $versionFlag" -o "./bin/rewst_remote_agent.win.exe" "./cmd/rewst_remote_agent"
go build -ldflags="-w -s $versionFlag" -o "./bin/rewst_windows_service.win.exe" "./cmd/rewst_windows_service"
go build -ldflags="-w -s $versionFlag" -o "./bin/rewst_agent_config.win.exe" "./cmd/rewst_agent_config"
go build -ldflags="-w -s $versionFlag" -o "./bin/rewst_service_manager.win.exe" "./cmd/rewst_service_manager"

# Create a build winres.json for patch
$winVersion = "$(cz version -p).0"
$winresObj = Get-Content -Path "./winres/winres.json" | Out-String | ConvertFrom-Json
$winresObj."RT_MANIFEST"."#1"."0409"."identity"."version" = $winVersion
$winresObj."RT_VERSION"."#1"."0000"."fixed"."file_version" = $winVersion
$winresObj."RT_VERSION"."#1"."0000"."fixed"."product_version" = $winVersion
$winresObj | ConvertTo-Json -Depth 16 -Compress | Out-File -FilePath "./bin/winres.json" -Encoding ASCII

# Copy the icon
Copy-Item "./winres/logo-rewsty.ico" "./bin/logo-rewsty.ico"

# Use the go-winres to patch the executables
go-winres patch --no-backup --in "./bin/winres.json" "./bin/rewst_remote_agent.win.exe"
go-winres patch --no-backup --in "./bin/winres.json" "./bin/rewst_windows_service.win.exe"
go-winres patch --no-backup --in "./bin/winres.json" "./bin/rewst_agent_config.win.exe"
go-winres patch --no-backup --in "./bin/winres.json" "./bin/rewst_service_manager.win.exe"
