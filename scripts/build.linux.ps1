# Build the executables for windows
$env:GOARCH = "amd64" # Use 64-bit as default architecture
$env:GOOS = "linux"
$versionFlag = "-X github.com/RewstApp/agent-smith-go/internal/version.Version=v$(cz version -p)"
go build -ldflags="-w -s $versionFlag" -o "./dist/rewst_agent_config.linux.bin" "./cmd/agent_smith"
