//go:build darwin

package main

// serviceManagerCommands is what the diagnostic bundle runs to capture the
// service manager's own view of the agent (sc-129690).
func serviceManagerCommands(name string) [][]string {
	return [][]string{{"launchctl", "print", "system/" + name}}
}
