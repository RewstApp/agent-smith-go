# Agent Smith
[![Test](https://github.com/RewstApp/agent-smith-go/actions/workflows/test.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/test.yml)
[![Coverage](https://github.com/RewstApp/agent-smith-go/actions/workflows/coverage.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/coverage.yml)
[![Lint](https://github.com/RewstApp/agent-smith-go/actions/workflows/lint.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/lint.yml)
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
- `--mqtt-qos`: MQTT subscription QoS level (`0` = at-most-once, `1` = at-least-once). Defaults to `1` when omitted. Azure IoT Hub does not support QoS 2.

Example with optional parameters:
```bash
./rewst_agent_config --org-id YOUR_ORG_ID --config-url CONFIG_URL --config-secret CONFIG_SECRET --logging-level info --syslog --disable-agent-postback --no-auto-updates --mqtt-qos 1
```

## Update

Once installed, the agent can be updated and configured using the config executable. The optional parameters are also available.

```bash
./rewst_agent_config --org-id YOUR_ORG_ID --update --logging-level info --syslog --disable-agent-postback --no-auto-updates --mqtt-qos 1
```

## Service Mode

Once configured, the agent can run in service mode using the generated configuration:
```bash
./rewst_agent_config --org-id YOUR_ORG_ID --config-file /path/to/config.json --log-file /path/to/agent.log
```

## Diagnostic Mode

The diagnostic mode provides an interactive menu to validate an installed agent without needing to inspect log files or know platform-specific service commands. It is useful for troubleshooting connectivity issues, verifying permissions, and confirming the agent is healthy.

### Usage

Run without an org ID to scan all installed agents:

**Windows:**
```cmd
rewst_agent_config.win.exe --diagnostic
```

**Linux:**
```bash
sudo ./rewst_agent_config.linux.bin --diagnostic
```

**macOS:**
```bash
sudo ./rewst_agent_config.mac-os.bin --diagnostic
```

To target a specific organization directly:

```bash
./rewst_agent_config --org-id YOUR_ORG_ID --diagnostic
```

### Interactive menu

Once launched, the menu guides you through the following checks:

```
[1] Scan installed agents and check status
[2] Test command execution
[3] Test MQTT/WebSocket connectivity
[4] Test temp directory write access
[5] View live log data
[6] Run all checks
[0] Exit
```

| Option | What it checks |
|--------|---------------|
| **1** | Lists all installed agents with running/stopped status and config details (device ID, IoT Hub, engine host, log level) |
| **2** | Runs a test command using the platform shell (PowerShell on Windows, Bash on Linux/macOS) and confirms execution succeeds |
| **3** | Attempts TLS connections to the agent's IoT Hub on port 8883 (MQTT) and port 443 (WebSocket). Prints troubleshooting tips if both fail |
| **4** | Creates a test file in the scripts temp directory and reads it back to confirm write access |
| **5** | Opens the agent log file and tails it in real time. Press Ctrl+C to stop |
| **6** | Runs checks 1–4 in sequence |

### Example output

```
  ╔══════════════════════════════════════════════════╗
  ║         Agent Smith Diagnostic Mode              ║
  ║         Version: v1.1.0                          ║
  ║         Platform: windows/amd64                  ║
  ╚══════════════════════════════════════════════════╝

  ── Installed Agents ──

    [PASS] a1b2c3d4-... - RUNNING (RewstRemoteAgent_a1b2c3d4-...)
      Device ID:    device-xyz
      IoT Hub:      abc123.azure-devices.net
      Engine Host:  engine.rewst.io
      Log Level:    info
      Syslog:       false
      Auto-Updates: true
      MQTT QoS:     1

  ── MQTT/WebSocket Connectivity ──

    Host: abc123.azure-devices.net
    Testing MQTT (TLS port 8883)... OK
    [PASS] MQTT TLS connection to abc123.azure-devices.net:8883
    Testing WebSocket (port 443)... OK
    [PASS] WebSocket connection to abc123.azure-devices.net:443
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

### Message Delivery Guarantee

Incoming messages are handed to a buffered queue drained by a pool of
command-execution workers. When that queue fills (a burst of commands, or
execution slow enough to keep every worker busy), the subscribe callback
**applies back-pressure instead of dropping the message**: it blocks until a
worker frees a slot. Because the agent subscribes at QoS 1 (at-least-once) by
default and the MQTT client only acknowledges a message after the callback
returns, a saturated agent stops acknowledging — so the broker holds and later
redelivers the command rather than the agent silently discarding it. This trades
a small amount of in-broker buffering for no silent command loss.

The only case where a message is discarded is when it arrives while a connection
cycle is tearing down (service stop or reconnect). The connection is going away
regardless, so at QoS ≥ 1 the broker redelivers on the next connection. These
drops are surfaced loudly — an `Error` log line, a cumulative dropped-message
counter, and a best-effort `AgentMessageDropped` plugin notification — rather
than a single warning, so they are observable in monitoring.

#### Tuning queue capacity and concurrency

Two optional fields in the device configuration file let high-volume deployments
tune the queue without code changes:

| Config key | Default | Description |
|------------|---------|-------------|
| `worker_count` | `10` | Number of concurrent command-execution workers draining the queue. |
| `message_queue_size` | `100` | Capacity of the buffered inbound message queue before back-pressure begins. |

Both fall back to their defaults when omitted or set to a non-positive value.
Raising `message_queue_size` absorbs larger bursts before back-pressure begins;
raising `worker_count` widens execution parallelism. Example snippet:

```json
{
  "worker_count": 25,
  "message_queue_size": 500
}
```

#### Bounding per-command execution time

By default a received command runs with no execution deadline: it is only
cancelled when the MQTT connection drops or the service stops. A script that
hangs (infinite loop, blocked on a prompt/`stdin`, stuck network call) therefore
occupies its worker indefinitely, and once as many hung commands accumulate as
there are workers the whole pool is exhausted and no further commands run until
the agent reconnects.

Set `command_timeout_seconds` to bound how long any single command may run:

| Config key | Default | Description |
|------------|---------|-------------|
| `command_timeout_seconds` | *(unset — unbounded)* | Maximum seconds a single command may run before it is killed and its worker released. |

When set to a positive value, each command runs under a derived context with
that deadline; if it is exceeded the command's process group is killed, the
worker is freed, and the result posted back is flagged with `"timed_out": true`
(distinct from a normal non-zero exit) while the event is logged at `Error`
level with the `post_id`. When omitted or set to a non-positive value, execution
stays unbounded, preserving the historical behavior. Example snippet:

```json
{
  "command_timeout_seconds": 300
}
```

#### Bounding per-command output size

A command's `stdout` and `stderr` are each captured through a **bounded writer**,
so a script that writes a very large volume of output cannot exhaust memory on
the endpoint and get the agent OOM-killed. Once a stream reaches its ceiling,
further bytes from that stream are discarded instead of accumulated, which keeps
the memory one command's output costs the agent a small constant multiple of the
ceiling no matter how much the script writes.

| Config key | Default | Description |
|------------|---------|-------------|
| `max_output_bytes` | `10485760` (10 MiB) | Maximum bytes of command output kept, applied independently to `stdout` and `stderr`. |

The default is far above any legitimate observed command result, so existing
workflows are unaffected. It falls back to the default when omitted or set to a
non-positive value, so the bound can never be disabled by configuration. The
value can also be set at install time with `--max-output-bytes <N>`, or changed
later with `--update --max-output-bytes <N>`.

Truncation is deliberately non-fatal:

- **The command is not killed for being verbose.** It runs to completion (or to
  its `command_timeout_seconds` deadline) with the excess output dropped, so a
  script whose real work succeeds still reports success.
- **The output produced before the ceiling was reached is still delivered** — a
  truncated result is never turned into an empty or error-only one.
- **The result says so explicitly**, so a workflow can tell a truncated result
  from a complete one instead of silently trusting a partial one:

```json
{
  "output": "...the first max_output_bytes of stdout...",
  "error": "...the first max_output_bytes of stderr...",
  "truncated": true,
  "output_bytes_produced": 2097152000,
  "output_bytes_kept": 20971520
}
```

`output_bytes_produced` and `output_bytes_kept` are totals across both streams.
All three keys are **omitted** when the output was captured in full, so a
complete result is byte-identical to what previous releases posted back. A
command that was both verbose and hung carries `"truncated": true` alongside
`"timed_out": true`.

Each truncation is logged **once per command** at `Warn` level with the
`message_id`, the ceiling in effect, and both byte counts — never once per write.

### Command Result Delivery

After a command runs, the agent posts its result back to the Rewst engine with
retry and exponential backoff. If every in-line attempt fails (network error or
`5xx` across the whole retry budget), the result is **not dropped**:

- The failure is surfaced beyond the log with a best-effort `AgentPostbackFailed:<post_id>`
  plugin notification so monitoring can observe it.
- The result is written to a **bounded on-disk spool** (under the agent's data
  directory) and re-attempted on the next successful connection cycle. A
  transient engine outage therefore recovers automatically once connectivity
  returns, instead of losing the result.

The spool is bounded by count and age (the oldest/expired entries are evicted)
so it cannot grow without limit, and the flush is bound to the connection cycle
so it never blocks shutdown.

The in-line retry budget is tunable per deployment:

| Config key | Default | Description |
|------------|---------|-------------|
| `postback_max_attempts` | `3` | Total postback attempts (including the first try) before the result is spooled. |
| `postback_base_retry_backoff_seconds` | `1` | Base delay for exponential backoff between attempts (`base * 2^(n-2)`). |

The per-attempt backoff is capped at **64s** (with up to ±25% jitter) regardless
of how high `postback_max_attempts` is raised, mirroring the reconnect backoff.
This keeps a wide retry window from overflowing into a busy-loop or blocking a
worker for days, so raising `postback_max_attempts` only ever adds more bounded
retry slots.

Both fall back to their defaults when omitted or set to a non-positive value, so
existing configurations are unaffected. Example snippet:

```json
{
  "postback_max_attempts": 6,
  "postback_base_retry_backoff_seconds": 2
}
```

### Staying Connected (SAS token renewal)

The agent authenticates to Azure IoT Hub with a short-lived SAS token, and the
hub forcibly disconnects a client the instant its token expires. To avoid a
forced disconnect on a fixed cadence, the agent mints a long-lived token and
proactively reconnects with a fresh one a safety margin **before** expiry. The
old connection is therefore never torn down by an expired token: the reconnect
is a routine, `Info`-level **`Renewing SAS token before expiry`** log line rather
than an `Error`-level `Connection lost`. An `Error` `Connection lost` now
reflects a genuine fault (network drop, broker-side disconnect), and reconnect
behavior for those real losses is unchanged.

| Config key | Default | Description |
|------------|---------|-------------|
| `sas_token_lifetime_hours` | `24` | Lifetime (in hours) of the Azure IoT Hub SAS token minted for each connection. |

The renewal margin is 10% of the lifetime, floored at 1 minute and capped at 15
minutes, so the token is used for almost its entire lifetime yet always refreshed
ahead of expiry. Falls back to the default when omitted or set to a non-positive
value. Example snippet:

```json
{
  "sas_token_lifetime_hours": 12
}
```

### Bounded MQTT Operations

Every MQTT operation in the connection cycle waits with a deadline, and the
subscribe wait is additionally interruptible by a service stop. Without that, a
broker which holds the connection open and answers keepalive pings but stops
responding to control packets — exactly what Azure IoT Hub does when it throttles
a device, and what a stateful firewall, VPN idle-timeout, captive portal, or
SD-WAN appliance that half-opens a connection produces — left the agent parked
mid-operation forever. It looked healthy (connected, keepalive fine, no errors
logged) while silently never subscribing, so every command sent to it was queued
at the broker and never ran, and a stop, upgrade, or uninstall hung until the
platform's stop deadline expired and force-killed the process.

With bounded waits, a broker in that state produces a clean, logged failure and a
normal reconnect instead:

- **Subscribe** — a SUBACK that does not arrive within the timeout is logged at
  `Error` as **`Failed to subscribe: timed out waiting for broker
  acknowledgement`** (with the timeout and elapsed wait), ends the cycle, and is
  retried on the reconnect backoff schedule. The backoff is deliberately **not**
  cleared, so a throttling broker gets progressively longer waits rather than
  being hammered. A stop arriving during the wait is honored immediately rather
  than after the timeout.
- **Connect** — bounded as a backstop above paho's own connect timeout, and
  likewise stop-interruptible.
- **Unsubscribe (teardown)** — bounded by a short fixed timeout so total teardown
  stays inside the Windows SCM stop window and systemd's `TimeoutStopSec`.
  Exceeding it logs a `Warn` and proceeds to disconnect; against a black-holed
  connection an unbounded wait would otherwise block until keepalive plus ping
  timeout elapsed, which alone exceeds the Windows default.
- **Device-twin reported properties** — bounded so the informational version
  report cannot wedge the cycle before the agent has subscribed. Its failure
  stays non-fatal (`Warn`).

Because the service now always exits through its own teardown rather than being
force-killed, deferred cleanup still runs: temp script files are removed, and the
spooled-postback and plugin subprocesses are shut down rather than orphaned.

| Config key | Default | Description |
|------------|---------|-------------|
| `mqtt_connect_timeout_seconds` | `30` | Bounds a single MQTT connect attempt. |
| `mqtt_subscribe_timeout_seconds` | `30` | How long to wait for the broker's SUBACK before treating the connection attempt as failed. |

Both fall back to their defaults when omitted or set to a non-positive value, and
can also be set at install or update time with `--mqtt-connect-timeout-seconds`
and `--mqtt-subscribe-timeout-seconds`. The teardown-side timeouts (unsubscribe,
device-twin publish) are fixed constants rather than per-device knobs, because a
tunable value there could push total teardown past a platform stop deadline the
operator cannot see. Example snippet:

```json
{
  "mqtt_subscribe_timeout_seconds": 45
}
```

### Bounded Windows Service Stop (Windows only)

Update, install and uninstall all stop the running service first, and on Windows
that means asking the Service Control Manager to stop it and then waiting for the
service to report `Stopped`. That wait used to be unbounded: it polled the service
state every 250 ms forever. A service that never reaches `Stopped` — a wedged
agent process, or Windows itself holding the service in `StopPending` behind a
stuck operation — therefore hung the caller indefinitely. Because the auto-updater
runs unattended, the visible symptom was a device that went offline during a
routine update with no error logged anywhere, needing hands on the endpoint to
recover; an interactive uninstall simply hung with no output.

The wait is now bounded:

- The stop waits at most **5 minutes** for the service to reach `Stopped`. The
  bound is deliberately generous — it exists to catch a wedge, not to race a
  normal shutdown, so a healthy agent draining long-running commands is never cut
  short.
- A service observed in `StopPending` keeps being polled until the deadline, while
  a state the service cannot reach `Stopped` from fails immediately rather than
  burning the full deadline. `Running` and `StartPending` are treated as
  still-stopping, because the SCM can report the pre-stop state before the service
  thread picks the control up.
- Overrunning the deadline logs at `Error` and names the service, the deadline and
  the last observed state — for example `service rewst_agent_smith_<org> did not
  stop within 5m0s: last observed state StopPending` — so the reason an update
  aborted is visible in the log.
- Callers abort instead of proceeding as if the stop succeeded. **Update** does
  not overwrite the agent executable or the config file, since the old process may
  still hold the executable open, leaving the existing installation intact and the
  service recoverable. **Uninstall** does not delete the registration or any files
  out from under a live process, and logs plainly that nothing was removed.
  **Install** (`--config`) likewise aborts before deleting a pre-existing wedged
  service.

Recovery is the ordinary one: end the wedged agent process, start the service, and
re-run the update or uninstall. Linux and macOS are unaffected — their service
implementations do not use this polling loop.

### Waiting for the Old Agent Process to Exit

Stopping the service is not the same as the old agent process being gone. Install,
update and uninstall used to bridge that gap with a fixed `time.Sleep` of five
seconds and then act regardless — overwriting the agent executable, or deleting
the installation directory. On a loaded endpoint the old process is frequently
still alive at the five second mark, because its shutdown legitimately drains the
commands in flight, tears down MQTT and kills its plugin subprocesses one at a
time. Windows then refuses to replace a running image, so the update failed with
a sharing violation *after* the service was already stopped, leaving the device
offline until someone intervened. Uninstall had the mirror-image problem: files
deleted out from under a live process, leaving an installation that neither ran
nor reinstalled.

Nothing is written or deleted now until the process is observed to be gone:

- The wait polls three **real signals**, all of which must clear: the service
  manager no longer reports the service active, no running process is executing
  the agent binary, and the executable is no longer held open as a running image
  (a sharing violation on Windows, `ETXTBSY` on Linux). The three overlap on
  purpose — the file signal is what actually blocks the write on Windows, but
  macOS permits writing to a running image, and a service manager can report a
  service stopped while its process is still winding down. An elapsed poll
  interval is never by itself treated as evidence of anything.
- It **returns as soon as the process is gone**, so a healthy update is faster
  than the unconditional five second sleep it replaces, not slower.
- It is bounded by a documented **2 minute** deadline, sized for a slow but
  legitimate shutdown (many workers, long-running commands, several plugin
  subprocesses). Overrunning it logs at `Error` what was still outstanding and for
  how long, and the caller aborts rather than proceeding.
- **Update** aborts before writing anything and leaves the installation fully
  intact, then **restarts the service it stopped** so a failed update never leaves
  the endpoint silently offline. **Uninstall** aborts before deleting the
  registration or any files, and logs that nothing was removed. **Install**
  (`--config`) aborts before deleting the existing registration and restarts the
  service it stopped.
- A probe that cannot run at all (a restrictive ACL, a process table that cannot
  be enumerated) is logged once at `Warn` and the remaining signals are used. A
  probe failure is not evidence a process is alive, and must not wedge every
  update on an endpoint where it can never succeed.

The agent executable and the config file are also written **atomically** — to a
temporary file in the destination directory, then renamed into place, the same
pattern the postback spool uses. An interrupted or failed write therefore leaves
the previous file byte-identical rather than truncated: the endpoint keeps running
the old agent instead of a binary that cannot start.

### Capped and Jittered Auto-Update Retries

When an update check or download fails, the agent retries on an exponential
schedule (base 5 minutes, doubling per retry, 5 retries) before waiting out the
next 48-hour check interval. Two bounds keep that schedule safe at fleet scale:

- **A cap.** No single retry sleep exceeds **1 hour**, or a quarter of the check
  interval when that is shorter (integration builds shorten the interval via
  ldflags). Without a ceiling the doubling reaches a 42-hour sleep by retry 10 —
  longer than the interval the retries are nested inside — and a large retry
  count overflows the doubling into a negative duration, which makes the wait
  return immediately and spin the retry loop against the release endpoint. The
  schedule is computed by iterated doubling with an early exit at the cap, so no
  intermediate value can overflow and every slot is strictly positive and
  bounded.
- **Jitter.** Each slot is spread by up to **±25%**, mirroring the reconnect and
  postback backoffs. An unjittered schedule makes every agent that failed at the
  same moment retry at the same instants, so a GitHub releases outage or rate
  limit turns the whole fleet into a synchronized retry storm that sustains the
  condition it is recovering from and keeps endpoints on older versions long
  after the outage ends. Jitter is applied after the cap, and a slot that would
  exceed the cap is reflected back under it rather than clamped to it — clamping
  would land half of every capped slot on exactly the ceiling, so a fleet held at
  the cap by a long outage would re-synchronize there.

The backoff wait stays interruptible by the service stop signal, so a stop is
never delayed by a pending retry, and a retry that succeeds resets the schedule
for the next cycle.

### Notification Plugin Supervision

Notification plugins run as separate subprocesses reached over RPC, and every
notification the agent sends is best effort — a delivery failure never interferes
with command execution. On its own that combination hides a plugin that dies:
once the subprocess exits, its RPC client stays broken forever and every later
notification (`AgentStatus:Online`/`Offline`/`Reconnecting`, `AgentPostbackFailed`,
`AgentMessageDropped`) silently goes nowhere.

Loaded plugins are therefore supervised for the lifetime of the service:

- A subprocess that exits or crashes is detected — by a health check that polls
  every 30s, and immediately on the notification path if one arrives first — and
  **relaunched**, so notifications resume without restarting the agent.
- Relaunches use an exponential backoff (5s up to 5 minutes) so a plugin that
  crashes on startup cannot turn into a process-spawn loop. A plugin that ran
  for at least 2 minutes before dying is treated as a one-off and relaunched
  immediately.
- Failures are observable instead of silent: a failed delivery is logged at
  `Error` level once per failure transition (with a matching recovery line), so a
  persistently broken plugin cannot flood the log, and cumulative counters for
  missed notifications, restarts and failed restarts are reported in a
  `Plugin notification health summary` line on shutdown.
- An error the plugin itself returns is counted and logged but leaves the healthy
  subprocess running; only a broken RPC channel triggers a relaunch.

Deployments with no plugins configured are unaffected — no supervision runs.

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

> **Note:** When running tests locally on Linux, some tests write to `/tmp/rewst_remote_agent/scripts`. If that directory was created by `root` (e.g., via `sudo`), your user won't have write access. Fix it by running:
> ```bash
> sudo chmod -R o+w /tmp/rewst_remote_agent
> ```

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
- ✅ Pass all linters
- ✅ Pass CodeQL security scanning

## Code Quality and Linting

Agent Smith uses [golangci-lint](https://golangci-lint.run/) for strict security and code formatting enforcement.

### Running Locally

#### **Install golangci-lint:**

See this [guide](https://golangci-lint.run/docs/welcome/install/local/) to learn how to install golangci-lint on your local machine.

#### **Run linter:**
```bash
golangci-lint run
```

#### **Auto-fix formatting:**
```bash
golangci-lint run --fix
```

### CI/CD

Linting runs automatically on:
- Every pull request
- Every push to main branch

## Contributing
Contributions are always welcome. Please submit a PR!

Please use commitizen to format the commit messages. After staging your changes, you can commit the changes with this command.

```
cz commit
```

## License

Agent Smith is licensed under `GNU GENERAL PUBLIC LICENSE`. See license file for details.
