# USED FOR LOCAL DEV ONLY
$orgId = $env:REWST_ORG_ID
Copy-Item -Path "./bin/rewst_remote_agent.win.exe" -Destination "C:/Program Files/RewstRemoteAgent/$orgId/rewst_remote_agent_$orgId.win.exe"
Copy-Item -Path "./bin/rewst_windows_service.win.exe" -Destination "C:/Program Files/RewstRemoteAgent/$orgId/rewst_windows_service_$orgId.win.exe"

sc.exe delete "RewstRemoteAgent_$orgId"
sc.exe create "RewstRemoteAgent_$orgId" binPath="C:/Program Files/RewstRemoteAgent/$orgId/rewst_windows_service_$orgId.win.exe"