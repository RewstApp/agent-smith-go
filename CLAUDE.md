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

  The install, update and uninstall paths never assume the old agent process has
  exited: after stopping the service they wait (bounded, 2 minutes, documented)
  for real exit signals — the service manager no longer reporting the service
  active, no process still executing the agent binary, and the executable no
  longer held open — and return as soon as the process is gone. Overrunning the
  deadline aborts before writing or deleting anything, leaves the installation
  intact, and restarts the service that was stopped so a failed update cannot
  leave an endpoint offline. The agent executable and config file are written to a
  temp file and atomically renamed into place, so a failed write leaves the
  previous file byte-identical. See the README's "Waiting for the Old Agent
  Process to Exit" section.

- **internal/agent/**: Device configuration, installation paths, and OS-specific host information. The auto-update retry schedule is capped (1 hour, or a quarter of the check interval when shorter) and jittered (±25%) via `utils.JitteredBackoff`, the same helper the postback retry schedule uses, so the doubling cannot overflow into a negative sleep that busy-spins and a fleet-wide release-endpoint outage cannot produce a synchronized retry storm. See the README's "Capped and Jittered Auto-Update Retries" section.
- **internal/interpreter/**: Command execution engine supporting both PowerShell and Bash interpreters  
- **internal/mqtt/**: Azure IoT Hub MQTT client implementation with auto-reconnection
- **internal/service/**: Cross-platform service management utilities. On Windows, `Stop()` waits a bounded, documented deadline (5 minutes) for the service to reach `Stopped` and otherwise returns an error naming the service and last observed state, so a wedged service aborts an update/install/uninstall with an actionable message instead of hanging forever. See the README's "Bounded Windows Service Stop" section.
- **internal/syslog/**: OS-specific system logging (Linux/macOS/Windows)
- **plugins/**: Plugin loader using HashiCorp's go-plugin framework
- **shared/**: Plugin interfaces and RPC definitions

### Plugin System

Agent Smith uses a plugin architecture for extensible notifications. Plugins are separate executables that implement the `Notifier` interface via RPC. The system supports loading multiple plugins simultaneously and sends status notifications (AgentStarted, AgentStatus:Online, AgentStatus:Offline, etc.) to all loaded plugins.

Loaded plugins are supervised: a subprocess that exits or crashes is detected (by a periodic health check and on the notification path) and relaunched with backoff, and delivery failures increment counters and are logged once per failure transition. See the README's "Notification Plugin Supervision" section.

### Message Processing Flow

1. Agent connects to Azure IoT Hub via MQTT on topic `devices/{device_id}/messages/devicebound/#`. Every MQTT operation waits with a deadline rather than an open-ended `token.Wait()`, and the connect/subscribe waits are additionally interruptible by the service stop signal, so a broker that keeps the connection open but stops acknowledging control packets (a throttling hub, a half-open middlebox) produces a logged failure and a backed-off reconnect instead of a device that is connected but never subscribed and cannot be stopped. See the README's "Bounded MQTT Operations" section.
2. Receives JSON messages containing either `commands` (shell scripts) or `get_installation` (system info requests)
3. Executes commands using platform-appropriate interpreter (PowerShell on Windows, Bash on Unix). Stdout and stderr are each captured through an independently bounded writer (`max_output_bytes`, default 10 MiB per stream) so a verbose script cannot OOM the agent; output past the ceiling is discarded and the result is flagged `truncated` with both byte counts. See the README's "Bounding per-command output size" section.
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