# Build the executables for windows
go build -ldflags="-w -s" -o "./bin/rewst_remote_agent.win.exe" "./cmd/rewst_remote_agent"
go build -ldflags="-w -s" -o "./bin/rewst_windows_service.win.exe" "./cmd/rewst_windows_service"
go build -ldflags="-w -s" -o "./bin/rewst_agent_config.win.exe" "./cmd/rewst_agent_config"
go build -ldflags="-w -s" -o "./bin/rewst_service_manager.win.exe" "./cmd/rewst_service_manager"

# Use the go-winres to patch
go-winres patch "./bin/rewst_remote_agent.win.exe"
go-winres patch "./bin/rewst_windows_service.win.exe"
go-winres patch "./bin/rewst_agent_config.win.exe"
go-winres patch "./bin/rewst_service_manager.win.exe"
