# Used for local developments
Copy-Item -Path "./bin/rewst_remote_agent.win.exe" -Destination "C:/Program Files/RewstRemoteAgent/$env:REWST_ORG_ID/rewst_remote_agent_$env:REWST_ORG_ID.win.exe"
Copy-Item -Path "./bin/rewst_windows_service.win.exe" -Destination "C:/Program Files/RewstRemoteAgent/$env:REWST_ORG_ID/rewst_windows_service_$env:REWST_ORG_ID.win.exe"
Copy-Item -Path "./bin/rewst_service_manager.win.exe" -Destination "C:/Program Files/RewstRemoteAgent/$env:REWST_ORG_ID/rewst_service_manager_$env:REWST_ORG_ID.win.exe"

sc.exe delete "RewstRemoteAgent_$env:REWST_ORG_ID"
sc.exe create "RewstRemoteAgent_$env:REWST_ORG_ID" binPath="C:/Program Files/RewstRemoteAgent/$env:REWST_ORG_ID/rewst_windows_service_$env:REWST_ORG_ID.win.exe --org-id $env:REWST_ORG_ID"
