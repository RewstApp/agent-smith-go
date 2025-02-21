# Build the executables for windows
go build -o "./bin/rewst_remote_agent.win.exe" "./cmd/rewst_remote_agent"
go build -o "./bin/rewst_windows_service.win.exe" "./cmd/rewst_windows_service"
go build -o "./bin/rewst_agent_config.win.exe" "./cmd/rewst_agent_config"
go build -o "./bin/rewst_service_manager.win.exe" "./cmd/rewst_service_manager"
