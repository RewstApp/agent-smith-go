# Used for local testing

# Put the executables to program files as in the installation
if ($null -eq $env:REWST_ORG_ID) {
    Write-Host "Missing REWST_ORG_ID"
    exit
}

# Copy to program files
Copy-Item -Path "./dist/rewst_remote_agent.win.exe" -Destination "$env:ProgramFiles\RewstRemoteAgent\$env:REWST_ORG_ID\rewst_remote_agent_$env:REWST_ORG_ID.win.exe"
Copy-Item -Path "./dist/rewst_service_manager.win.exe" -Destination "$env:ProgramFiles\RewstRemoteAgent\$env:REWST_ORG_ID\rewst_service_manager_$env:REWST_ORG_ID.win.exe"
Copy-Item -Path "./dist/rewst_windows_service.win.exe" -Destination "$env:ProgramFiles\RewstRemoteAgent\$env:REWST_ORG_ID\rewst_windows_service_$env:REWST_ORG_ID.win.exe"

# Leave one copy in mocking for easy control
Copy-Item -Path "./dist/rewst_windows_service.win.exe" -Destination "./mock/rewst_windows_service_$env:REWST_ORG_ID.win.exe"

# Put the data files
Copy-Item -Path "./mock/config.json" -Destination "$env:ProgramData\RewstRemoteAgent\$env:REWST_ORG_ID\config.json"
