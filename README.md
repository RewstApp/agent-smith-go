# Agent Smith
[![Test](https://github.com/RewstApp/agent-smith-go/actions/workflows/test.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/test.yml)
[![Coverage](https://github.com/RewstApp/agent-smith-go/actions/workflows/coverage.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/coverage.yml)
[![CodeQL](https://github.com/RewstApp/agent-smith-go/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/github-code-scanning/codeql)
[![Release](https://github.com/RewstApp/agent-smith-go/actions/workflows/release.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/release.yml)

Rewst's lean, open-source command executor that fits right into your Rewst workflows. See [community corner](https://docs.rewst.help/documentation/agent-smith) for more details.

## Installation

Agent Smith runs as a system service on Windows, Linux, and macOS. Installation involves configuring the agent with your organization credentials and starting the service.

### Prerequisites

- A Rewst organization ID
- Configuration URL and secret from your Rewst platform
- Administrative/root privileges for service installation

### Basic Installation

1. Download the appropriate binary for your platform from the [releases page](https://github.com/RewstApp/agent-smith-go/releases)
2. Configure the agent with your organization credentials:

**Windows:**
```cmd
rewst_agent_config.win.exe --org-id YOUR_ORG_ID --config-url CONFIG_URL --config-secret CONFIG_SECRET
```

**Linux/macOS:**
```bash
./rewst_agent_config.linux.bin --org-id YOUR_ORG_ID --config-url CONFIG_URL --config-secret CONFIG_SECRET
# or
./rewst_agent_config.mac-os.bin --org-id YOUR_ORG_ID --config-url CONFIG_URL --config-secret CONFIG_SECRET
```

### Configuration Options

- `--logging-level`: Set logging verbosity (`info`, `warn`, `error`, `debug`)  
- `--syslog`: Write logs to system log instead of file (Linux/macOS)
- `--disable-agent-postback`: Disable agent postback
- `--no-auto-updates`: Disable auto updates

Example with optional parameters:
```bash
./rewst_agent_config --org-id YOUR_ORG_ID --config-url CONFIG_URL --config-secret CONFIG_SECRET --logging-level info --syslog --disable-agent-postback --no-auto-updates
```

## Update

Once installed, the agent can be updated and configured using the config executable. The optional parameters are also available.

```bash
./rewst_agent_config --org-id YOUR_ORG_ID --update --logging-level info --syslog --disable-agent-postback --no-auto-updates
```

## Service Mode

Once configured, the agent can run in service mode using the generated configuration:
```bash
./rewst_agent_config --org-id YOUR_ORG_ID --config-file /path/to/config.json --log-file /path/to/agent.log
```

## Uninstallation

To remove Agent Smith from your system:

```bash
# Replace with your organization ID
./rewst_agent_config --org-id YOUR_ORG_ID --uninstall
```

This will stop the service, remove configuration files, and clean up system service registrations.

## Features

- **Cross-platform**: Runs on Windows, Linux, and macOS
- **Secure**: Uses Azure IoT Hub MQTT for encrypted communication
- **Extensible**: Plugin system for custom notifications and integrations
- **Reliable**: Automatic reconnection and error handling
- **Lightweight**: Minimal resource footprint

## How It Works

1. Agent connects to your Rewst organization via Azure IoT Hub MQTT
2. Receives command execution requests from Rewst workflows
3. Executes commands using PowerShell (Windows) or Bash (Unix/Linux/macOS)
4. Returns results back to the Rewst platform
5. Supports system information collection and custom plugins

## Build
Required tools and packages:

- [commitizen](https://commitizen-tools.github.io/commitizen/): To use a standardized description of commits.
  ```
  pipx ensurepath
  pipx install commitizen
  pipx upgrade commitizen
  ```

- [go-winres](https://github.com/tc-hib/go-winres): To embed icons and file versions to windows executables.
  ```
  go install github.com/tc-hib/go-winres@latest
  ```

Run the following command using `powershell` or `pwsh` to build the binary:
```
./scripts/build.ps1
```

## Testing and Coverage

Agent Smith maintains high code quality through comprehensive testing with an **80% coverage threshold**.

### Running Tests

**Run all tests:**
```bash
go test ./...
```

**Run tests with verbose output:**
```bash
go test ./... -v
```

**Run tests for a specific package:**
```bash
go test ./cmd/agent_smith -v
go test ./internal/service -v
go test ./plugins -v
```

**Run a specific test:**
```bash
go test ./cmd/agent_smith -v -run TestLoadConfig
```

### Coverage Reports

**Generate coverage report:**
```bash
./scripts/coverage.ps1
```

This script:
- Runs tests across all packages
- Generates coverage profiles
- **Enforces 80% minimum coverage threshold**

### Test Categories

**Unit Tests**: Test individual functions and components in isolation
- Message parsing and validation
- Configuration loading
- SAS token generation
- Path resolution

**Integration Tests**: Test component interactions
- MQTT message flow (with test broker)
- Service lifecycle (start/stop/restart)
- Plugin loading and notifications
- Command execution and postback

**Platform-Specific Tests**: Test OS-specific functionality
- Windows service management
- Linux systemd integration
- macOS launchd integration
- System information collection

### Writing Tests

When contributing new code, ensure:

1. **Test coverage**: Aim for >80% coverage for new code
2. **Table-driven tests**: Use for multiple test cases
   ```go
   tests := []struct {
       name     string
       input    string
       expected string
   }{
       {"case1", "input1", "expected1"},
       {"case2", "input2", "expected2"},
   }
   ```
3. **Clean up resources**: Use `t.TempDir()` and `defer` statements
4. **Avoid flaky tests**: Use proper synchronization and timeouts
5. **Mock external dependencies**: Don't rely on network or filesystem in unit tests

### CI/CD

Tests run automatically on:
- Every pull request
- Every push to main branch
- Pre-release validation

**GitHub Actions Workflows:**
- `.github/workflows/test.yml` - Runs test suite
- `.github/workflows/coverage.yml` - Validates coverage threshold

**Pull requests must:**
- ✅ Pass all tests
- ✅ Maintain ≥80% coverage
- ✅ Pass CodeQL security scanning

## Contributing
Contributions are always welcome. Please submit a PR!

Please use commitizen to format the commit messages. After staging your changes, you can commit the changes with this command.

```
cz commit
```

## License

Agent Smith is licensed under `GNU GENERAL PUBLIC LICENSE`. See license file for details.