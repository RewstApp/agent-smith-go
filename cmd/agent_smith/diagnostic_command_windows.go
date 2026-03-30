//go:build windows

package main

func getTestCommand() (string, []string) {
	return "powershell", []string{"-Command", "Write-Output 'Agent Smith diagnostic test'"}
}
