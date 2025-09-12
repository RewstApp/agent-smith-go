# Agent Smith Architecture

This document provides technical details about Agent Smith's architecture and client system deployment. For installation instructions and user guides, see [README.md](README.md).

## Overview

Agent Smith is a lightweight, cross-platform agent that enables remote command execution through Rewst workflows. Each agent connects to Azure IoT Hub via MQTT, receives commands from the Rewst platform, executes them locally, and returns results.

## Agent Architecture

### Core Components

- **Agent Service**: Main process that handles MQTT communication and command execution
- **Configuration System**: Device credentials, MQTT endpoints, and logging settings
- **Command Interpreters**: Platform-specific execution engines (PowerShell/Bash)
- **Plugin System**: Extensible notification and integration framework
- **System Integration**: Service management and system information collection

### Communication Flow

```
Rewst Platform → Azure IoT Hub (MQTT) → Agent Smith → Local System
      ↑                                                        ↓
Results ← HTTP Webhook ← Agent Smith ← Command Execution
```

1. **Connection**: Agent establishes secure MQTT connection to Azure IoT Hub
2. **Message Reception**: Receives JSON commands on topic `devices/{device_id}/messages/devicebound/#`
3. **Command Execution**: Runs commands using appropriate interpreter (PowerShell/Bash)
4. **Result Posting**: Sends execution results back via HTTP webhook to Rewst platform
5. **Reconnection**: Automatic reconnection with exponential backoff on connection loss

## Client System Deployment

Agent Smith deploys differently based on the operating system, with organization-specific directory structures.

### Windows Deployment

**Installation Paths** (`internal/agent/installation_windows.go:11-57`):
- **Program Directory**: `%PROGRAMFILES%\RewstRemoteAgent\{ORG_ID}\`
- **Data Directory**: `%PROGRAMDATA%\RewstRemoteAgent\{ORG_ID}\`  
- **Scripts Directory**: `%SYSTEMDRIVE%\RewstRemoteAgent\scripts\{ORG_ID}\`

**Key Files**:
- **Executable**: `agent_smith.win.exe` in Program Directory
- **Configuration**: `config.json` in Data Directory
- **Logs**: `rewst_agent.log` in Data Directory
- **Service Name**: `RewstRemoteAgent_{ORG_ID}`

### Linux Deployment

**Installation Paths** (`internal/agent/installation_linux.go:11-57`):
- **Program Directory**: `/usr/local/bin/rewst_remote_agent/{ORG_ID}/`
- **Data Directory**: `/etc/rewst_remote_agent/{ORG_ID}/`
- **Scripts Directory**: `/tmp/rewst_remote_agent/scripts/{ORG_ID}/`

**Key Files**:
- **Executable**: `agent_smith.linux.bin` in Program Directory
- **Configuration**: `config.json` in Data Directory
- **Logs**: `rewst_agent.log` in Data Directory
- **Service Name**: `rewst_remote_agent_{ORG_ID}`

### macOS Deployment

**Installation Paths** (`internal/agent/installation_darwin.go:11-57`):
- **Program Directory**: `/usr/local/bin/rewst_remote_agent/{ORG_ID}/`
- **Data Directory**: `/Library/Application Support/rewst_remote_agent/{ORG_ID}/`
- **Scripts Directory**: `/tmp/rewst_remote_agent/scripts/{ORG_ID}/`

**Key Files**:
- **Executable**: `agent_smith.mac-os.bin` in Program Directory
- **Configuration**: `config.json` in Data Directory
- **Logs**: `rewst_agent.log` in Data Directory
- **Service Name**: `io.rewst.remote_agent_{ORG_ID}`

## Configuration Structure

The agent uses a JSON configuration file containing:

```json
{
  "device_id": "unique-device-identifier",
  "rewst_org_id": "organization-id",
  "rewst_engine_host": "engine.rewst.io",
  "shared_access_key": "azure-iot-hub-key",
  "azure_iot_hub_host": "hub.azure-devices.net",
  "broker": "mqtt-broker-url",
  "logging_level": "info|warn|error|off",
  "syslog": true|false,
  "plugins": [
    {
      "name": "plugin-name",
      "executable_path": "/path/to/plugin"
    }
  ]
}
```

## System Information Collection

Agent Smith collects comprehensive system information (`internal/agent/host.go:16-98`):

**Hardware & OS**:
- Hostname, MAC address, operating system
- CPU model, RAM capacity
- Agent version and installation paths

**Directory Services Integration**:
- Active Directory domain membership
- Domain controller detection
- Azure AD (Entra) domain information
- Entra Connect server detection

## Plugin System

### Plugin Architecture

Plugins extend agent functionality through HashiCorp's go-plugin framework (`plugins/loader.go:87-127`):

- **Plugin Interface**: RPC-based communication via `shared.Notifier` interface
- **Lifecycle Management**: Automatic loading, execution, and cleanup
- **Multi-Plugin Support**: Multiple plugins can run simultaneously
- **Error Isolation**: Plugin failures don't crash the main agent

### Plugin Communication

```go
type Notifier interface {
    Notify(message string) error
}
```

**Notification Events**:
- `AgentStarted`: Agent initialization complete
- `AgentStatus:Online`: Successfully connected to MQTT
- `AgentStatus:Offline`: Connection lost
- `AgentStatus:Reconnecting`: Attempting reconnection
- `AgentStatus:Stopped`: Agent shutdown
- `AgentReceivedMessage:{payload}`: Command received

### Plugin Deployment

Plugins are standalone executables deployed alongside the agent:
- **Location**: Specified in agent configuration `plugins[].executable_path`
- **Execution**: Launched as separate processes with RPC communication
- **Security**: Isolated execution with magic cookie authentication

## Command Execution

### Interpreter Selection

Commands execute using platform-appropriate interpreters (`internal/interpreter/common.go:109-121`):

- **Windows**: PowerShell (default) or override to Bash
- **Linux/macOS**: Bash (default) or override to PowerShell
- **Override**: `interpreter_override` field in command messages

### Execution Flow

1. **Message Parsing**: JSON command deserialized from MQTT payload
2. **Interpreter Selection**: Platform detection or explicit override
3. **Command Execution**: Shell command execution with output capture
4. **Result Formatting**: JSON response with stdout, stderr, and exit code
5. **Postback**: HTTP POST to Rewst webhook endpoint

### Security Considerations

- **Isolation**: Commands run in separate process contexts
- **Logging**: All command execution logged for audit
- **Authentication**: MQTT connection secured with Azure IoT Hub keys
- **Transport**: Encrypted MQTT and HTTPS communication

## Service Management

Each platform uses native service management:

- **Windows**: Windows Service Manager
- **Linux**: systemd service
- **macOS**: launchd service

Services run with appropriate system privileges to enable comprehensive system management and command execution.

## Monitoring and Observability

### Logging System

- **Structured Logging**: JSON-formatted log entries with contextual information
- **Log Levels**: Configurable verbosity (info, warn, error, off)
- **Multiple Outputs**: File-based logging with optional syslog integration
- **Rotation**: Platform-specific log rotation policies

### Status Reporting

- **Connection Status**: Real-time MQTT connection state
- **Command Execution**: Success/failure status and execution time
- **System Health**: Resource usage and service status
- **Plugin Status**: Individual plugin health and error states

## Operational Modes

For detailed installation and usage instructions, refer to the [README.md](README.md). The agent supports three operational modes:

### Installation Mode
Creates service installation and generates device configuration from Rewst platform.

### Service Mode  
Main operational mode as system service that connects to MQTT and processes commands with automatic reconnection.

### Uninstall Mode
Cleanly removes agent service, configuration files, and service registration.