// Package workflowlint pins structural rules on .github/workflows/
// integration-test.yml that the suite's own history shows are easy to break
// by copy-paste and expensive to find afterwards. It runs under a plain
// `go test ./...` so the test workflow enforces them on every PR.
package workflowlint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func workflow(t *testing.T) []string {
	t.Helper()
	p := filepath.Join("..", "..", ".github", "workflows", "integration-test.yml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return strings.Split(string(b), "\n")
}

type step struct {
	name string
	line int
	uses string
	cond string
	args string // the args: value, continuation lines joined with spaces
}

var (
	nameRe = regexp.MustCompile(`^\s*- name: (.*)$`)
	usesRe = regexp.MustCompile(`^\s*uses: \./\.github/actions/([a-z-]+)`)
	ifRe   = regexp.MustCompile(`^\s*if: (.*)$`)
	argsRe = regexp.MustCompile(`^(\s*)args: ?(.*)$`)
)

// steps returns every step that calls a local composite action, with its args
// value assembled across YAML folded/continuation lines.
func steps(t *testing.T) []step {
	t.Helper()
	var out []step
	var cur *step
	inArgs, argsIndent := false, 0
	for i, ln := range workflow(t) {
		if m := nameRe.FindStringSubmatch(ln); m != nil {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &step{name: m[1], line: i + 1}
			inArgs = false
			continue
		}
		if cur == nil {
			continue
		}
		if m := usesRe.FindStringSubmatch(ln); m != nil {
			cur.uses = m[1]
		}
		if m := ifRe.FindStringSubmatch(ln); m != nil {
			cur.cond = m[1]
		}
		if m := argsRe.FindStringSubmatch(ln); m != nil {
			argsIndent = len(m[1])
			cur.args = strings.TrimSpace(m[2])
			inArgs = true
			if strings.HasPrefix(cur.args, ">") || strings.HasPrefix(cur.args, "|") {
				cur.args = "" // folded/literal block: the value is on the lines below
			}
			continue
		}
		if inArgs {
			indent := len(ln) - len(strings.TrimLeft(ln, " "))
			if strings.TrimSpace(ln) != "" && indent > argsIndent {
				cur.args = strings.TrimSpace(cur.args + " " + strings.TrimSpace(ln))
				continue
			}
			inArgs = false
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// A matrix value spliced into the agent's arguments varies by platform
// invisibly: the step reads the same on every OS, the resolved command does
// not, and a platform-only red is then misattributed to the agent. Platform
// differences belong in `if:` conditions on explicit steps.
func TestNoMatrixValueIsSplicedIntoAgentArgs(t *testing.T) {
	for _, s := range steps(t) {
		if s.uses != "run-agent" {
			continue
		}
		if strings.Contains(s.args, "${{ matrix.") {
			t.Errorf("line %d, step %q: args splice a matrix value: %s", s.line, s.name, s.args)
		}
	}
}

// The matrix must not grow another platform-conditional flag bag either.
func TestMatrixDefinesNoExtraFlagValues(t *testing.T) {
	re := regexp.MustCompile(`(?i)^\s+\w*extra\w* = `)
	for i, ln := range workflow(t) {
		if re.MatchString(ln) {
			t.Errorf(
				"line %d: matrix defines a flag bag %q; pass the flag explicitly in the step that needs it",
				i+1,
				strings.TrimSpace(ln),
			)
		}
	}
	for i, ln := range workflow(t) {
		if strings.Contains(ln, "no_auto_updates_extra") ||
			strings.Contains(ln, "NO_AUTO_UPDATES_EXTRA") {
			t.Errorf("line %d: no_auto_updates_extra is back: %s", i+1, strings.TrimSpace(ln))
		}
	}
}

// --disable-agent-postback only bites on Windows (the PowerShell executor's
// AlwaysPostback() is false there). Exactly one step verifies it, and that
// step must say it is Windows-only, so no other scenario runs with the agent
// postback disabled on one platform.
func TestDisableAgentPostbackIsExplicitAndWindowsOnly(t *testing.T) {
	var carriers []step
	for _, s := range steps(t) {
		if strings.Contains(s.args, "--disable-agent-postback") {
			carriers = append(carriers, s)
		}
	}
	if len(carriers) != 1 {
		for _, s := range carriers {
			t.Logf("line %d: %s", s.line, s.name)
		}
		t.Fatalf(
			"%d run-agent steps pass --disable-agent-postback, want exactly 1 (the verification scenario)",
			len(carriers),
		)
	}
	s := carriers[0]
	if !strings.Contains(s.cond, "windows") {
		t.Errorf(
			"line %d, step %q passes --disable-agent-postback without a Windows-only `if:` (got %q)",
			s.line,
			s.name,
			s.cond,
		)
	}
	if !strings.Contains(strings.ToLower(s.name), "windows") {
		t.Errorf(
			"line %d, step %q should name windows in its title so the platform difference is visible in the job summary",
			s.line,
			s.name,
		)
	}
}

// The Windows-only script that re-runs an update by hand must not smuggle a
// matrix value in through the environment either.
func TestNoScriptStepSplicesAMatrixFlagBagThroughEnv(t *testing.T) {
	re := regexp.MustCompile(`^\s+[A-Z_]*EXTRA[A-Z_]*: \$\{\{ matrix\.`)
	for i, ln := range workflow(t) {
		if re.MatchString(ln) {
			t.Errorf("line %d: %s", i+1, strings.TrimSpace(ln))
		}
	}
}
