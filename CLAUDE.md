# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Agent Smith is a lean, open-source command executor that integrates with Rewst workflows. It's written in Go and designed to run as a system service on Windows, Linux, and macOS client systems. The agent connects to Azure IoT Hub via MQTT to receive and execute commands remotely.

## Build Commands

Use PowerShell 7+ for building on all platforms:

```powershell
# Build for current platform
./scripts/build.ps1

# Clean build artifacts  
./scripts/clean.ps1

# Generate test coverage report
./scripts/coverage.ps1
```

The build script automatically detects the platform and creates platform-specific binaries in the `./dist/` directory:
- Windows: `rewst_agent_config.win.exe`
- Linux: `rewst_agent_config.linux.bin` 
- macOS: `rewst_agent_config.mac-os.bin`

## Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

## Development Dependencies

Required tools:
- [commitizen](https://commitizen-tools.github.io/commitizen/): `pipx install commitizen`
- [go-winres](https://github.com/tc-hib/go-winres): `go install github.com/tc-hib/go-winres@latest` (Windows builds only)

## Architecture

### Core Components

- **cmd/agent_smith/**: Main application entry point with three operational modes:
  - Configuration mode: `--config-url --config-secret --org-id`
  - Service mode: `--config-file --log-file --org-id` 
  - Uninstall mode: `--uninstall --org-id`

- **internal/agent/**: Device configuration, installation paths, and OS-specific host information
- **internal/interpreter/**: Command execution engine supporting both PowerShell and Bash interpreters  
- **internal/mqtt/**: Azure IoT Hub MQTT client implementation with auto-reconnection
- **internal/service/**: Cross-platform service management utilities
- **internal/syslog/**: OS-specific system logging (Linux/macOS/Windows)
- **plugins/**: Plugin loader using HashiCorp's go-plugin framework
- **shared/**: Plugin interfaces and RPC definitions

### Plugin System

Agent Smith uses a plugin architecture for extensible notifications. Plugins are separate executables that implement the `Notifier` interface via RPC. The system supports loading multiple plugins simultaneously and sends status notifications (AgentStarted, AgentStatus:Online, AgentStatus:Offline, etc.) to all loaded plugins.

### Message Processing Flow

1. Agent connects to Azure IoT Hub via MQTT on topic `devices/{device_id}/messages/devicebound/#`
2. Receives JSON messages containing either `commands` (shell scripts) or `get_installation` (system info requests)
3. Executes commands using platform-appropriate interpreter (PowerShell on Windows, Bash on Unix)
4. Posts results back to Rewst engine at `https://{rewst_engine_host}/webhooks/custom/action/{post_id}`

### Client System Deployment

The agent runs as a system service with these key files:
- Configuration file: Contains device credentials, MQTT endpoints, logging settings, and plugin configurations
- Log file: Application logs (with optional syslog integration)
- Plugin executables: Located at paths specified in device configuration
- Service binary: Platform-specific executable installed as system service

## Commit Convention

Use commitizen for standardized commit messages:
```bash
# Stage changes then commit
cz commit
```

Version management follows semantic versioning via commitizen in `.cz.toml`.