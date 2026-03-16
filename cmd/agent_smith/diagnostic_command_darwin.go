//go:build darwin

package main

func getTestCommand() (string, []string) {
	return "bash", []string{"-c", "echo 'Agent Smith diagnostic test'"}
}
