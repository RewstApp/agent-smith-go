#! /usr/bin/pwsh

# Get the operating system description
$osDescription = [System.Runtime.InteropServices.RuntimeInformation]::OSDescription

if ($osDescription -like "*Windows*") { 
    go build -o "./bin/rewst_remote_agent.exe" "./cmd/rewst_remote_agent"
} else {
    go build -o "./bin/rewst_remote_agent" "./cmd/rewst_remote_agent"
}
