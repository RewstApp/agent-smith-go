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
	name  string
	line  int
	uses  string
	cond  string
	args  string // the args: value, continuation lines joined with spaces
	block string // the step's full text
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
		cur.block += ln + "\n"
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

// ── bounds (sc-117886) ───────────────────────────────────────────────────────

func readWorkflow(t *testing.T, name string) []string {
	t.Helper()
	p := filepath.Join("..", "..", ".github", "workflows", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return strings.Split(string(b), "\n")
}

// jobs returns each job's name and the lines of its block.
func jobs(lines []string) map[string][]string {
	out := map[string][]string{}
	var cur string
	inJobs := false
	for _, ln := range lines {
		if ln == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if m := regexp.MustCompile(`^  ([a-z-]+):$`).FindStringSubmatch(ln); m != nil {
			cur = m[1]
			continue
		}
		if cur != "" {
			out[cur] = append(out[cur], ln)
		}
	}
	return out
}

// A job without timeout-minutes runs to GitHub's six-hour default, and under the
// integration-test concurrency group that blocks every dispatch behind it. A
// job that delegates to a reusable workflow (`uses:`) carries no bound of its
// own, so the reusable workflow's job must carry it instead.
func TestEveryIntegrationJobIsBounded(t *testing.T) {
	for name, block := range jobs(workflow(t)) {
		body := strings.Join(block, "\n")
		if regexp.MustCompile(`(?m)^    uses: \./\.github/workflows/`).MatchString(body) {
			continue // bounded inside the reusable workflow; checked below
		}
		if !strings.Contains(body, "timeout-minutes:") {
			t.Errorf("job %q has no timeout-minutes", name)
		}
	}
	for name, block := range jobs(readWorkflow(t, "build.yml")) {
		if !strings.Contains(strings.Join(block, "\n"), "timeout-minutes:") {
			t.Errorf(
				"build.yml job %q has no timeout-minutes (the integration `build` job reuses it)",
				name,
			)
		}
	}
}

// Every step that waits on something outside the runner - the agent binary, the
// engine, a stub server, a log line - carries its own bound below the job's, so
// a hang is attributed to the step in the job summary rather than surfacing as
// "exceeded the maximum execution time" on the job.
func TestEveryWaitingStepIsBounded(t *testing.T) {
	waiting := regexp.MustCompile(
		`(?m)^\s+uses: \./\.github/actions/(install-agent|run-agent|send-command|wait-for-log-line|stub-[a-z]+|wedged-service|assert-log-[a-z]+)$|it-scripts/(wait-for|assert)`,
	)
	lines := workflow(t)
	starts := []int{}
	for i, ln := range lines {
		if strings.HasPrefix(ln, "      - name: ") {
			starts = append(starts, i)
		}
	}
	for k, s := range starts {
		e := len(lines)
		if k+1 < len(starts) {
			e = starts[k+1]
		}
		block := strings.Join(lines[s:e], "\n")
		if waiting.MatchString(block) && !strings.Contains(block, "timeout-minutes:") {
			t.Errorf(
				"line %d, step %s waits on an external system without timeout-minutes",
				s+1,
				strings.TrimPrefix(lines[s], "      - name: "),
			)
		}
	}
}

// A curl without --max-time can wait on a half-open connection forever; the
// step bound would catch it, but only after the whole budget is spent. Only
// curl *commands* count - first word of a line, or after $( ( ; | && - so an
// input description that mentions curl is not a finding.
func TestEveryCurlHasMaxTime(t *testing.T) {
	root := filepath.Join("..", "..", ".github")
	command := regexp.MustCompile(`(^|\$\(|\(|;|\||&&)\s*curl\s`)
	continuation := regexp.MustCompile(`\\\n\s*`)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".yml") && !strings.HasSuffix(p, ".sh") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		text := continuation.ReplaceAllString(string(b), " ")
		for i, ln := range strings.Split(text, "\n") {
			trim := strings.TrimSpace(ln)
			if strings.HasPrefix(trim, "#") || !command.MatchString(trim) {
				continue
			}
			if !strings.Contains(trim, "--max-time") {
				t.Errorf("%s:%d: curl without --max-time: %s", p, i+1, trim)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ── waits are event-relative (sc-117884) ────────────────────────────────────

// A wait-for-log-line without a baseline passes the instant any earlier cycle
// left a matching line, so it waits for nothing and the step after it races
// the real event. Every wait must carry baseline_count captured before the
// step that produces the line.
func TestEveryWaitForLogLineHasABaseline(t *testing.T) {
	for _, s := range steps(t) {
		if s.uses != "wait-for-log-line" {
			continue
		}
		if !strings.Contains(s.block, "baseline_count:") {
			t.Errorf("line %d, step %q: wait-for-log-line without baseline_count", s.line, s.name)
		}
	}
}

// assert-log-contains polls with a timeout; a fixed sleep before a single read
// is the flake this replaced. The input no longer exists, so any use is a typo
// that GitHub would silently ignore.
func TestNoFixedSleepBeforeAnAssertion(t *testing.T) {
	for i, ln := range workflow(t) {
		if strings.Contains(ln, "wait_seconds:") {
			t.Errorf(
				"line %d: wait_seconds is gone; assert-log-contains polls (timeout_seconds) - %s",
				i+1,
				strings.TrimSpace(ln),
			)
		}
	}
}

// A sleep in an inline script is only acceptable as the interval of a bounded
// poll loop. A bare sleep that gates the next assertion is a fixed delay.
func TestInlineSleepsAreInsidePollLoops(t *testing.T) {
	lines := workflow(t)
	sleepRe := regexp.MustCompile(`^\s+(Start-Sleep|sleep)\s`)
	loopRe := regexp.MustCompile(`\b(for|while|until|foreach)\b`)
	for i, ln := range lines {
		if !sleepRe.MatchString(ln) {
			continue
		}
		// Look back within the same run: block for a loop header.
		inLoop := false
		for j := i - 1; j >= 0 && j > i-40; j-- {
			if regexp.MustCompile(`^\s+run: \|`).MatchString(lines[j]) {
				break
			}
			if loopRe.MatchString(lines[j]) {
				inLoop = true
				break
			}
		}
		if !inLoop {
			t.Errorf("line %d: sleep outside a poll loop: %s", i+1, strings.TrimSpace(ln))
		}
	}
}
