# Build the executables for windows

go build -o "./bin/rewst_remote_agent.win.exe" "./cmd/rewst_remote_agent"
go build -o "./bin/rewst_windows_service.win.exe" "./cmd/rewst_windows_service"
