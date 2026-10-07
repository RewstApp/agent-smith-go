// Package sleeplint pins the rule that a unit test may not synchronise with a
// goroutine, or assert that nothing happened, by sleeping a guessed number of
// milliseconds. Every time.Sleep in a *_test.go must carry a `// sleep-ok:`
// justification on the same or the preceding line - a poll interval inside a
// bounded loop, a deliberately aged artefact, a helper-process body - so the
// next bare sleep is a review conversation rather than a flake found on a
// loaded runner (sc-119840).
package sleeplint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEverySleepInATestIsJustified(t *testing.T) {
	root := filepath.Join("..", "..")
	var findings []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "dist", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "sleeplint_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(b), "\n")
		for i, ln := range lines {
			if !strings.Contains(ln, "time.Sleep(") ||
				strings.HasPrefix(strings.TrimSpace(ln), "//") {
				continue
			}
			if strings.Contains(ln, "sleep-ok:") ||
				(i > 0 && strings.Contains(lines[i-1], "sleep-ok:")) {
				continue
			}
			rel, _ := filepath.Rel(root, p)
			findings = append(findings, rel+":"+itoa(i+1)+": "+strings.TrimSpace(ln))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("time.Sleep without a `// sleep-ok:` justification: %s", f)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
