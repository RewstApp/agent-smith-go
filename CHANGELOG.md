## v1.2.4 (2026-04-30)

### Fix

- [sc-89439] Move defer notifier.Kill() past error check to prevent panic (#63)
- [sc-89438] Guarantee MQTT client cleanup on all reconnect cycle exit paths (#62)
- [sc-89435] Buffer lost channel to prevent OnConnectionLost deadlock (#61)
- [sc-89434] Close msgQueue per reconnect cycle to prevent goroutine leak (#60)
- [sc-89690] Missing Release Title in Release Workflow (#59)

## v1.2.3 (2026-04-20)

### Fix

- silent json errors on result (#56)
- [sc-89003] PowerShell Profile Output Contaminates Host Info Fields (#55)
- [sc-87866] Error Context Lost in Host Info #54)
- [sc-87865] Hardcoded MQTT QoS (#53)
- [sc-87907] Out-of-Bounds Panic in extractMessage — syslog.go (#52)
- [sc-87906] Temp File Leaked on Failed Command Execution (#51)
- [sc-87905] `defer client.Disconnect()` Inside Reconnection Loop Leaks MQTT Clients (#50)

## v1.2.2 (2026-04-10)

### Fix

- [sc-86631] New `http.Client` Created Per Postback — Connection Pool Bypass (#49)
- [sc-86628] Duplicate Signal/Context Setup Across Darwin and Linux Service Files (#48)
- [sc-86627] Orphaned Temp File on Updater Failure (#47)
- [sc-86626] No Jitter in Reconnection Backoff — Thundering Herd Risk (#46)
- [sc-86624] Wrong Error Logged on MQTT Subscription Failure (#45)
- [sc-86623] Agent Hangs Indefinitely — HTTP Requests Without Timeout (#44)
- [sc-86620] Panic Risk — Unsafe Type Assertion in Plugin Loader (#43)
- [sc-86616] Goroutine Leak — Unbounded MQTT Message Processing (#42)

## v1.2.1 (2026-04-03)

### Fix

- [sc-86007] Add config validation in config mode (#41)

## v1.2.0 (2026-03-30)

### Feat

- [sc-81851] Diagnostic mode (#36)

### Fix

- [sc-85690] Auto updater integration test (#39)
- [sc-84570] Outdated agent smith version tag (#38)
- add always postback to executor (#37)

## v1.1.1 (2026-03-16)

### Fix

- [sc-83534] Auto updater fails to restart service (#35)

## v1.1.0 (2026-03-14)

### Feat

- [sc-81850] Macos integration test (#34)
- [sc-81849] Linux integration test (#33)
- [sc-81848] Windows integration test (#32)

## v1.0.0 (2026-03-03)

### Feat

- [sc-68094] Test code coverage improvement and added linter (#31)

## v0.16.3 (2026-01-27)

### Fix

- [sc-78990] Update eclipse mqtt library (#30)

## v0.16.2 (2025-11-13)

### Fix

- [sc-73140] Use git tag for versioning (#29)

## v0.16.1 (2025-10-30)

### Fix

- [sc-35889] Add no-auto-updates support on update mode (#28)

## v0.16.0 (2025-10-30)

### Feat

- [sc-35889] Automatic updates (#27)

## v0.15.1 (2025-10-23)

### Fix

- [sc-67562] Agent incorrectly interprets utf-16le data as utf-8 (#26)

## v0.15.0 (2025-10-14)

### Feat

- [sc-70245] Add whoami check (#25)
- [sc-70245] Add pwsh shell support (#24)

## v0.14.1 (2025-10-09)

### Fix

- [sc-69853] Improve debug messaging for command execution (#23)

## v0.14.0 (2025-10-03)

### Feat

- [sc-44407] Include agent version # in headers (#21)

### Fix

- [sc-69368] Missing error message on failed update (#22)

## v0.13.0 (2025-09-26)

### Feat

- add debug logs for bash
- add support for disable_agent_postback in config and update mode
- add update mode
- add disable_agent_postback config
- add debug log for commands
- add debug logs for stdout and stderr values

### Fix

- improve format of shell version log output
- show shell version on debug mode

## v0.12.0 (2025-08-28)

### Feat

- add plugins config
- transfer handshake config via cli args
- update agent status message format
- add support for plain message
- implement basic plugin notification
- add web socket hub for real time messaging
- add monitoring local server

### Fix

- go mod tidy
- kill plugin process and log loaded plugins
- cleanup plugin loader
- typo
- remove unused http server
- merge conflict errors
- apply go mod tidy

### Refactor

- use optional notifier plugin wrapper

## v0.11.0 (2025-08-15)

### Feat

- save the syslog parameter to config file
- syslog support for linux and darwin
- implement windows syslog sub package
- convert hostname to lowercase

### Fix

- service is active status method
- cant stop service from launchctl
- duplicate install on windows syslog

## v0.10.0 (2025-08-08)

### Feat

- use bash as default interpreter for non-windows runtimes
- add support for darwin-specific functions

### Fix

- modify path env in macos service plist
- wrong config file parameter in macos service
- missing check on service open for macos

## v0.9.0 (2025-07-23)

### Feat

- add logging-level option to config mode
- replace remaining log calls with logger
- integrate configurable logger to config mode
- integrate configurable logging on uninstall routine
- integrate configurable logger to service mode

### Fix

- apply go mod tidy

## v0.4.5 (2025-07-21)

### Fix

- suppress errors on host info fields

## v0.4.4 (2025-07-21)

### Fix

- return error and output strings as result

### Refactor

- postback response log messages

## v0.4.3 (2025-07-18)

### Fix

- show device id on service logs
- allow false on message interpreter_override field

## v0.4.2 (2025-06-02)

### Fix

- add permissions on jobs with workflow call
- add minimal permissions block on jobs
- add permissions to build workflow

## v0.4.1 (2025-05-27)

### Fix

- permissions on release job

## v0.4.0 (2025-05-27)

### Feat

- config overwrites existing service
- get device id step
- add host linux test
- add output directory creation step
- add got test output and stop command for failed test
- add coverage script
- add file hash step
- wrong name on first asset
- use first file for code signing
- add sign reusable workflow
- add uninstall linux routine
- add linux config routine
- add linux service routine

### Fix

- add missing checking on linux service open method
- add windows test procedure
- update required flag for the build input
- update strategy matrix os value
- update os list for integration test
- use os_list json data type
- remove excess symbol in config secret
- add config secret var
- make binary executable
- add list dist folder step
- add step to check if sudo is executable
- add whoami commnad
- add sudo echo command
- add sudo ls check
- add full path sudo
- use full path for executable
- remove sudo
- use realpath for the installer
- check if sudo is available
- exclude new line character in os list json
- try ubuntu only run
- remove trailing space
- use inputs context variable
- show list of files recursively on install
- add list command
- set working directory for test coverage step
- add azure iot hub unit test
- make prod as default environment name in sign workflow
- add checkout ref
- skip code signing on linux binary
- add fail-fast parameter on strategy
- restore ubuntu for sign workflow
- add secrets parameters to sign workflow
- add secrets to sign workflow
- add a write step to secret
- use raw username
- use secrets variable for username
- change command to batch_sign
- hard code es codesign environment variables
- use sign command with set filename
- use full path in batch sign step
- use sslcom action for batch sign step
- improve steps
- update batch sign code step
- adjust sequence of steps
- add debug on missing directory
- add debug path for code sign
- use ssl com signing script
- update input file path
- add debug on extract first asset
- add debug for downloaded binary directory
- add debug value on first asset extract
- create signed directory step
- typo on codesign command
- add codesign missing credentials and parameters
- add execute permission on code sign
- update signer parameters
- add checkout step on sign
- add needs param on sign
- add missing upload job on qa
- update artifact upload name
- add python setup step
- append command to env file
- add env file write to build script
- add missing environment input
- remove typo in go build script for linux
- change crlf to lf for build scripts
- fix shell on build step
- set shell on build step to powershell
- update shell on linux build step
- update linux build script description

### Refactor

- return lastexitcode on coverage script
- convert default file mods to const
- common methods in service routine
- common methods in uninstall routine
- common methods in config routine
- remove unused scripts

## v0.3.0 (2025-03-18)

### Feat

- add uninstall function

### Fix

- add wait time for service executable to stop before deleting
- add polling interval constant to wait for stopped service status
- add client disconnect quiesce constant
- add response status code log in config
- remove service management and add uninstall header
- remove bump on staging workflow
- upgrade dependencies
- add uninstall flag to remove service and files

### Refactor

- used flagset to scope flags for a specific routine
- separate routines from the main file

## v0.2.0 (2025-03-02)

### Feat

- add postback mechanism
- add reconnection when connection is lost
- add installation process
- add agent smith executable for all functions

### Fix

- add postback description
- missing installation informationo
- update build workflow to match new executables
- remove stdout and stderr redirection
- remove log fatal and service manager
- stop service messages

## v0.1.0 (2025-02-24)

### Feat

- add fetch config body param
- improve service manager
- use executable as source of org id
- add command dispatch result

### Fix

- adjust logs and command handling
- update remote agent command line args
- adjust logs and command handling
- update remote agent command line args
- cleanup the data
- remove stray log in org id from executable
- adjust logs and command handling
- update remote agent command line args
- append dist folder to sign script
- add app dist path as param
- update dist directory
- add missing ssl com signing script
- update workflow condition
- missing filenames
- update compilation path and integrate build gh workflow
- add version management and update build scripts
- output log to stdout
- upgrade to go 1.24.0
- add icon and version to build system
- add command line arg for service executable
- move paths retrieval to main function
- rewst windows service executable
- agent config executable
- use agent scripts directory
- use log in command execution status
- save config file after fetch
- implement agent config adjustment
- remove separate postback feature
- adjust error reporting and interpreter
- reconnect routine
- use context for cancellatio within the subscription
- add support for context in subscription
- support strip trigger during connection and subscription
