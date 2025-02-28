# Used for local testing

# Put the executables to program files as in the installation
if ($null -eq $env:REWST_ORG_ID) {
    Write-Host "Missing REWST_ORG_ID"
    exit
}

# Compute the paths
$filesDirectory = "$env:ProgramFiles\RewstRemoteAgent\$env:REWST_ORG_ID"
$dataDirectory = "$env:ProgramData\RewstRemoteAgent\$env:REWST_ORG_ID"
$agentSmithPath = "$filesDirectory\agent_smith.win.exe"
$configPath = "$dataDirectory\config.json"
$logPath = "$dataDirectory\rewst_agent.log"

# Copy to program files
Copy-Item -Path "./dist/agent_smith.win.exe" -Destination $agentSmithPath

# Put the data files
Copy-Item -Path "./mock/config.json" -Destination $configPath

# Register the service
$name = "RewstRemoteAgent_$env:REWST_ORG_ID"
sc.exe delete $name
sc.exe create $name binPath= "$agentSmithPath --org-id $env:REWST_ORG_ID --config-file `"$configPath`" --log-file `"$logPath`""
