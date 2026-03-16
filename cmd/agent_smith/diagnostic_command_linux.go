//go:build linux

package main

func getTestCommand() (string, []string) {
	return "bash", []string{"-c", "echo 'Agent Smith diagnostic test'"}
}
