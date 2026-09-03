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

This will stop the service, remove configuration files, and clean up system
service registrations. If a directory cannot be removed — a file held open by an
AV scanner, for example — the remaining directories are still removed and the
paths that survived are named in the log (see "Completing Uninstall When a
Directory Cannot Be Removed" below).

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

Every received command runs under an execution deadline, on by default, so a
script that hangs (infinite loop, blocked on a prompt/`stdin`, stuck network
call) cannot occupy its worker indefinitely. Without a bound, once as many hung
commands accumulate as there are workers the whole pool is exhausted and no
further commands run until the agent reconnects.

Set `command_timeout_seconds` to override how long any single command may run:

| Config key | Default | Description |
|------------|---------|-------------|
| `command_timeout_seconds` | `1800` (30 minutes) | Maximum seconds a single command may run before it is killed and its worker released. |

Each command runs under a derived context with that deadline; if it is exceeded
the command's process group is killed, the worker is freed, and the result
posted back is flagged with `"timed_out": true` (distinct from a normal
non-zero exit) while the event is logged at `Error` level with the `post_id`.
It falls back to the default when omitted or set to a non-positive value, so
the bound can never be disabled by configuration; raise it for workflows with
legitimately long-running commands. Example snippet:

```json
{
  "command_timeout_seconds": 300
}
```

#### Killing the Full Process Tree on Windows

Killing a command's process on timeout or cancellation must also kill whatever
that command spawned — a `Start-Process` call, an installer, a stuck helper —
or the child is reparented and keeps running on the endpoint after the
worker is released, leaking a process per hang. On Unix this is handled by
placing the shell in its own process group and killing the group
(`internal/interpreter/proc_unix.go`); Windows has no process-group
equivalent, so the same guarantee is provided with a **job object**
(`internal/interpreter/proc_windows.go`): the shell process is assigned to a
job created with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, and closing the job's
handle — which cancellation now does instead of a plain `Process.Kill` —
terminates every process still assigned to it, direct or descendant. The job
handle is released once the command finishes either way, so a command that
completes normally leaks no handle.

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

#### Hardening the Command Scripts Directory

Each received command is written to a temporary script file before it is
executed. On Linux and macOS, that file used to be written to a subdirectory
of the shared, world-writable system temp directory
(`os.TempDir()/rewst_remote_agent/scripts/<orgId>`). Because directory
creation is a no-op against a directory that already exists, any local
unprivileged user could pre-create that directory — or simply wait for a
`tmpfs` `/tmp` to reset on reboot — with permissive ownership and mode before
the agent ever ran, and the agent would silently reuse it exactly as found.
Combined with the write, close, and path-based re-open the executor used to
hand the script to the shell, a local user able to write into that directory
could in principle swap the script's contents in the gap between the two
opens and have it run with the agent's privilege (root/SYSTEM).

Three changes address this, matching the precedent already set for
auto-update installers (see "Reclaiming Downloaded Installer Binaries") where
they apply:

- **On Linux and macOS, scripts land in a directory the agent owns.** The
  scripts directory is now `<data directory>/scripts`
  (`/etc/rewst_remote_agent/<orgId>/scripts`,
  `/Library/Application Support/rewst_remote_agent/<orgId>/scripts`) instead
  of the shared system temp directory — a location an unprivileged local user
  cannot pre-create in the first place.

  **Windows deliberately keeps its historical location**,
  `C:\RewstRemoteAgent\scripts\<orgId>` (the system drive root, not under
  `ProgramData`), and does not follow this move. Some customers have their
  endpoint security software configured to whitelist exactly this path so the
  dynamically-written PowerShell scripts the agent executes here are not
  flagged or blocked as they run; relocating it would silently break command
  execution on those endpoints. It also was not the vulnerability described
  above to begin with — that depended on a shared, world-writable temp
  directory, and an unprivileged user cannot create a new top-level directory
  at the Windows system drive root under the OS's default ACLs.
- **Its permissions are re-asserted on every command, not only when first
  created.** `EnsureSecureDir` (`internal/utils/filesystem_unix.go`,
  `filesystem_windows.go`) runs before every command, not only the first, on
  all three platforms. On Linux and macOS it refuses to follow a symlink or
  non-directory planted at that path, reclaims ownership via `chown` if the
  directory belongs to another uid, and re-applies mode `0700` if it does not
  already have it — failing loud rather than proceeding if either correction
  itself fails. A directory that already has the right owner and mode is left
  untouched. On Windows, where there are no POSIX ownership/mode bits to
  re-assert, it only refuses a symlink or non-directory planted at that path.
- **The write-then-exec window is verified, not just trusted.** After the
  script file is written and closed, its contents are read back and compared
  byte-for-byte against what was just written; a mismatch aborts the command
  instead of executing whatever is now on disk. This is defense in depth on
  top of the directory hardening above — on Linux/macOS, with the directory
  locked to `0700` and owned by the agent's own account, no other local user
  can write into it at all, so the window this check exists for should never
  legitimately fire there.

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

#### One undeliverable result never blocks the rest

A flush distinguishes two failures that look alike from a distance:

- **The engine is unreachable** — a transport error, or a connection that broke
  while the response was being read. Nothing behind this entry could be
  delivered either, so the flush stops and every remaining entry is retried on a
  later cycle. This costs the entry nothing: an outage is not the entry's fault
  and consumes none of its attempt budget.
- **The engine rejected this entry** — it answered with a `5xx`, or with a body
  that could not be parsed. The engine is plainly up, so the flush **passes over
  this entry and keeps going**; every entry behind it still gets its attempt.

The second case used to be read as the first. One result the engine consistently
rejected would pin the queue: the flush restarted from it every cycle, retried
it forever with no attempt bound, and the healthy results behind it were never
attempted until the age check discarded them — logged as `expired`, which reads
like stale-data cleanup rather than the delivery failure it was.

An entry that is rejected carries a **persisted attempt counter and last error**,
so its budget survives an agent restart. After **5 counted rejections** the entry
is abandoned: removed with the distinct reason `attempts_exhausted`, counted
separately, and surfaced with a best-effort `AgentPostbackAbandoned:<post_id>`
plugin notification — never silently reported as stale.

Rejections are counted **at most once every 10 minutes**. An engine that is
failing wholesale answers `5xx` for every entry, which at the HTTP layer is
indistinguishable from it rejecting each one specifically; without that spacing,
a flapping connection could spend an entry's whole budget in minutes and abandon
a result the engine would have accepted on recovery. Spacing bounds the budget in
time rather than in reconnects, so a result survives at least 40 minutes of a
wholesale outage however often the agent reconnects. It never delays the pass-over
itself — a rejected entry is skipped on every flush regardless; only the counting
is spaced.

Drop reasons are counted separately (`expired`, `capacity`, `attempts_exhausted`,
`corrupt`) so a spool shedding entries under pressure is distinguishable in
diagnostics from one abandoning a result the engine refuses. Spool entry files
written by an older agent have no attempt counter; they are read as
never-attempted and delivered normally, not discarded.

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

### Bounded Service and Host-Info Shell-Outs (macOS, Linux, Windows)

The Windows service `Stop()` wait above bounds one path to a wedged OS-level
tool. Several adjacent shell-outs used to have no bound at all: `launchctl` on
macOS, `systemctl` on Linux, and `sc query` / `dsregcmd` / the WMI queries
PowerShell issues on Windows host-info gathering. A D-Bus stall, launchd in a
bad state, or a broken WMI repository — a known real-world failure mode,
especially on domain controllers — blocked install, update, uninstall and
config generation indefinitely, the same failure class already fixed for
`Stop()`, just left open here.

Every one of these commands is now run with `exec.CommandContext` under a
bounded, documented timeout:

- **macOS** (`internal/service/service_darwin.go`): every `launchctl`
  invocation (`load`, `start`, `stop`, `unload`, `print`) is bounded by
  `launchctlTimeout`, **5 minutes** — the same bound as the Windows service
  stop wait, generous enough to never cut off a legitimate stop.
- **Linux** (`internal/service/service_linux.go`): every `systemctl`
  invocation (`start`, `stop`, `enable`, `disable`, `is-active`, `is-enabled`,
  `daemon-reload`) is bounded by `systemctlTimeout`, also **5 minutes**.
- **Windows** (`internal/agent/host_windows.go`): the WMI queries behind
  `ADDomain`/`IsADDomainController`, the `sc query` calls behind
  `IsEntraConnectServer`, and the `dsregcmd /status` call behind `EntraDomain`
  are each bounded by `hostCommandTimeout`, **30 seconds** — these normally
  complete in well under a second, and the caller-supplied context passed in
  from `config.go`/`update.go` carries no deadline of its own, so the bound has
  to be enforced internally rather than relied on from the caller.

A command that exceeds its bound is killed and returns an error naming the
command and its arguments (for example `systemctl stop rewst_agent_smith_<org>
timed out after 5m0s: ...`), which callers treat the same as any other failure
from that command — an install/update/uninstall aborts without writing or
deleting anything, and host-info gathering logs the field as unavailable
(`NewHostInfo` already warns per-field and continues) rather than blocking the
rest of the flow.

All three timeouts are overridable via `-ldflags` for integration testing —
`service.launchctlTimeoutOverrideStr`, `service.systemctlTimeoutOverrideStr`,
and `agent.hostCommandTimeoutOverrideStr` — the same mechanism
`stopTimeoutOverrideStr` uses for the Windows service stop wait, so a wedged
command can be observed aborting in seconds rather than the production bound.

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

### Completing Uninstall When a Directory Cannot Be Removed

Uninstall removes three directories: the org's data directory, its program
directory, and its scripts directory. It used to remove them sequentially and
return on the first `RemoveAll` failure, after the service registration had
already been deleted. A single locked file — an AV scanner mid-scan, a stale
open handle on Windows, both routine during uninstall — therefore orphaned every
directory behind the one that failed, potentially tens of megabytes, with no
service registration left to retry the uninstall cleanly through.

Every directory is now attempted **independently**:

- A failure on one directory is logged with its path and the removal moves on to
  the next, so the directories that *can* be removed always are.
- The failures are reported together at the end, at `Error`, **naming every path
  that survived** (`failed_directories`, plus `failed_count` of
  `attempted_count`), so whoever picks up the cleanup does not have to guess
  which directories were reached. A clean uninstall logs `Uninstall completed`.
- The order stays data, program, scripts. On Linux and macOS the scripts
  directory lives *inside* the data directory, so removing the data directory
  normally takes it along and the later attempt is a no-op (`RemoveAll` on a
  missing path succeeds); when the data directory removal fails, that separate
  attempt is a second, narrower chance to reclaim the scripts underneath it.

This applies only to removing the installed files. Everything before that — a
service that will not stop, an agent process that will not exit, a registration
that cannot be deleted — still aborts the uninstall with nothing removed, since
those failures mean the agent may still be running (see "Waiting for the Old
Agent Process to Exit" above).

### Surviving Its Own systemd Stop (Linux)

The `--update` helper that an auto-update spawns has to stop the running
service before it can replace the binary and start it again. On Linux that
service runs as a systemd unit, and systemd's default `KillMode=control-group`
tears down every process in the unit's cgroup — not just its main process —
when the unit is stopped. The helper is launched as a child of the running
service, so without intervention it inherits that cgroup: the moment it calls
`systemctl stop` on its own unit, systemd kills the helper along with the
service it just asked to stop, mid-update. The service is left stopped, the
binary and config were never touched, and — because the kill is a signal, not
a normal return — the helper's own deferred recovery never runs either.
`Restart=always` does not help: the unit was stopped by an explicit
`systemctl stop`, which systemd treats as a clean, intentional exit, not the
unexpected one `Restart=` reacts to.

The helper now runs inside its own transient systemd **scope**
(`systemd-run --scope --collect`) rather than as a plain child process, so it
is never a member of the unit's cgroup in the first place. Stopping the unit
it was launched from tears down only that unit's cgroup; the helper's scope is
untouched, so it survives to replace the binary, update the config, and start
the service again — the same flow already used on Windows and macOS. macOS
needed no equivalent change: launchd tears down a stopped job by BSD process
group (`killpg`), and the `Setsid` the helper already sets moves it into a new
process group, which is enough to escape that teardown. Linux's cgroup-based
`KillMode` is inherited across `fork()` and untouched by `setsid()`, so the
same call that protects the helper on macOS does not protect it on Linux.

### Verified, Version-Gated Auto-Updates

Every auto-update downloads a full agent binary and executes it as the
installer, so two questions have to be answered before that binary is trusted:
is it actually the release the agent asked for, and is it actually newer than
what is already running? Neither was checked before.

- **Checksum verification.** `Download` hashes the installer as it streams to
  disk and aborts — removing the temp file it already cleans up on any other
  download failure — unless the hash matches the SHA-256 digest GitHub's
  Releases API returns natively for that asset (`Asset.Digest`, format
  `sha256:<hex>`): GitHub computes this itself, server-side, from the bytes it
  received when the release was published, so there is nothing our own release
  job has to compute or upload alongside the binary. A missing digest, one
  using an algorithm other than sha256, or one that isn't a well-formed
  64-character hex string fails the same way: verification is required, not
  best-effort, so a corrupted-but-200-OK download, a tampered release asset, or
  a broken release job can never be executed. (An earlier version of this
  mechanism instead published a hand-computed `<binary-name>.sha256` sidecar
  asset per binary — `.github/workflows/sign.yml` still emits it for now as a
  safety net for agents built before this change, which keep expecting it on
  every future release until they themselves update, but nothing in the
  current agent reads it.)
- **Newer-than, not not-equal.** `Run` used to update whenever the latest tag
  differed from the running version at all. That also updates on an *older*
  tag — a release process mistake that republishes or points the check
  endpoint at a stale release would silently downgrade the whole fleet. The
  comparison is now a proper semantic version check (`isNewerVersion`): an
  update proceeds only when the latest release's `MAJOR.MINOR.PATCH` is greater
  than the running version's, and a tag that fails to parse aborts the check
  with an error instead of being guessed at in either direction.
- **A size ceiling on the download.** `Download` bounds the installer to 200 MiB
  regardless of the `Content-Length` header (which can be absent or wrong) —
  generous headroom over the compiled binary's actual size, so a legitimate
  release is never at risk, while a misbehaving or compromised release endpoint
  cannot fill the updates directory, and the volume under it, by serving an
  oversized or endless body. `downloadTimeout` already bounds how long the
  request runs; this bounds how many bytes it can deliver in that time.

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

### Reclaiming Downloaded Installer Binaries

Every auto-update downloads a full agent binary and executes it as the installer.
That file has to survive the download — the installer is spawned detached and the
process that could delete it afterwards is the one the installer replaces — so
the agent cannot clean up after itself on the update path. Nothing else did
either, so one orphaned binary (tens of megabytes) accumulated per update for the
lifetime of the installation. On the space-constrained systems where that matters
most — thin VDI images, small VM system disks, appliances — a full temp volume is
not just an agent problem: it breaks Windows Installer, application logging, and
anything else that needs scratch space, and the agent's own next update fails
because it can no longer allocate a temp file.

Two changes reclaim the space:

- **Downloads land in a directory the agent owns.** Installers are written to
  `<data directory>/updates` (`C:\ProgramData\RewstRemoteAgent\<orgId>\updates`,
  `/etc/rewst_remote_agent/<orgId>/updates`,
  `/Library/Application Support/rewst_remote_agent/<orgId>/updates`) instead of
  the shared system temp directory, with the directory created `0700`. A full
  agent binary is no longer left executable and world-readable, the sweep below
  only ever runs against a directory this agent created, and endpoints that mount
  `/tmp` `noexec` — a common hardening baseline — can execute the installer at
  all. Uninstall already removes the data directory wholesale, so nothing is left
  behind.
- **A startup sweep removes what previous updates left.** On every service start,
  after the service has reported itself running, installer binaries older than
  **24 hours** are removed. Because a successful update restarts the agent, each
  start reclaims the previous update's installer and leaves the current one
  alone, so steady-state usage is a single file rather than one per update. The
  legacy shared temp directory is swept as well, so an upgraded endpoint reclaims
  everything it has accumulated since it was installed rather than only stopping
  the growth from here on.

The sweep is deliberately conservative, matching the existing stale-script sweep:
only regular files (never symlinks or device nodes) whose name is exactly the
`installer-<digits>.bin` pattern `os.CreateTemp` produces, and only those past the
age threshold — which is why it is safe to point at a directory shared with the
rest of the system. The pattern is a shared constant used by both the download
and the sweep, so the two cannot drift. It is best effort throughout: an
unreadable directory or an unremovable file (a Windows installer still running
holds its own image open) is logged and skipped, never failing or delaying agent
startup. A non-zero number of removals is logged at `Info` with the count and
directory; individual removals are logged at `Debug`.

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

### Bounded Notification Plugin RPC Calls

The health check above only detects a subprocess that has actually **exited** —
it reads an in-process exit flag and performs no RPC. A plugin whose process is
still alive but has deadlocked internally (blocked on a channel that's never
signaled, stuck on a downstream call) is invisible to it, and the `Notify` RPC
call itself used to have no deadline: `net/rpc`'s `Call` blocks until a response
arrives, however long that takes. Since every message and status transition
calls `Notify` on every loaded plugin, one plugin hanging without crashing could
silently exhaust the whole worker pool over time — command execution stalling
fleet-wide with MQTT connectivity still looking perfectly healthy, and no error
anywhere pointing at the cause.

Every `Notify` call is now bounded by a **10 second** timeout, matching the
deadline pattern already used for MQTT operations:

- A call that does not return within the timeout is abandoned and treated as a
  plugin failure — counted and logged once per failure transition, exactly like
  a crash — rather than blocking the calling worker any longer.
- The failure is tracked in its own counter, separate from other notify
  failures, so a hang is observable as distinct from a crash or a plugin-
  returned error in both the logs and the `Plugin notification health summary`.
- Because the subprocess is still alive at this point, dropping the handle also
  **kills it** (the same teardown `Kill` uses), so the next `Notify` or health
  check relaunches a fresh subprocess on the existing backoff schedule instead
  of repeatedly timing out against a permanently wedged process.

The timeout is a fixed constant rather than a device config knob: unlike the
MQTT timeouts, it bounds RPC to a subprocess the host itself launched on the
same machine, not a network endpoint an operator might need to tune.

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
