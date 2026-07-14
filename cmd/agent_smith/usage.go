package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// flagHeaderPrefix matches the single-dash flag header that flag.PrintDefaults
// emits at the start of each flag line (two spaces followed by one dash). The
// agent documents flags with a double dash everywhere else, so the rendered
// flag list is rewritten to match (e.g. "-diagnostic" -> "--diagnostic").
var flagHeaderPrefix = regexp.MustCompile(`(?m)^  -`)

// renderFlagDefaults returns a flag set's per-flag descriptions using the
// double-dash form so the help output matches how the flags are invoked.
func renderFlagDefaults(fs *flag.FlagSet) string {
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.PrintDefaults()
	return string(flagHeaderPrefix.ReplaceAll(buf.Bytes(), []byte("  --")))
}

// operationalMode describes a single command-line mode for the purposes of
// argument detection and usage rendering. The modes are listed in the same
// order main attempts their context constructors.
type operationalMode struct {
	// name is the human-readable mode name (e.g. "config").
	name string
	// selector is the primary flag (without leading dashes) that signals the
	// operator's intent to use this mode (e.g. "config-url").
	selector string
	// summary is the one-line invocation fragment shown in the usage summary.
	summary string
	// flagSet returns a fresh flag set whose flags carry this mode's per-flag
	// usage descriptions. The bound params are discarded; only the flag
	// definitions are used for rendering.
	flagSet func() *flag.FlagSet
}

// operationalModes lists every mode in dispatch order. The summaries mirror the
// historical one-line usage string so existing behavior is preserved.
func operationalModes() []operationalMode {
	configFlagsList := fmt.Sprintf(
		"[--logging-level %s] [--syslog] [--disable-agent-postback] [--no-auto-updates] [--mqtt-qos 0|1] [--mqtt-connect-timeout-seconds <N>] [--worker-count <N>] [--message-queue-size <N>] [--postback-max-attempts <N>] [--postback-base-retry-backoff-seconds <N>] [--service-username <USER>] [--service-password <PASS>]",
		getAllowedConfigLevelsString("|"),
	)

	return []operationalMode{
		{
			name:     "diagnostic",
			selector: "diagnostic",
			summary:  "--org-id <ORG_ID> --diagnostic",
			flagSet:  func() *flag.FlagSet { return newDiagnosticFlagSet(&diagnosticContext{}) },
		},
		{
			name:     "uninstall",
			selector: "uninstall",
			summary:  "--org-id <ORG_ID> --uninstall",
			flagSet:  func() *flag.FlagSet { return newUninstallFlagSet(&uninstallContext{}) },
		},
		{
			name:     "config",
			selector: "config-url",
			summary: fmt.Sprintf(
				"--org-id <ORG_ID> --config-url <CONFIG URL> --config-secret <CONFIG SECRET> %s",
				configFlagsList,
			),
			flagSet: func() *flag.FlagSet { return newConfigFlagSet(&configContext{}) },
		},
		{
			name:     "service",
			selector: "config-file",
			summary:  "--org-id <ORG_ID> --config-file <CONFIG FILE> --log-file <LOG FILE>",
			flagSet:  func() *flag.FlagSet { return newServiceFlagSet(&serviceContext{}) },
		},
		{
			name:     "update",
			selector: "update",
			summary:  fmt.Sprintf("--org-id <ORG_ID> --update %s", configFlagsList),
			flagSet:  func() *flag.FlagSet { return newUpdateFlagSet(&updateContext{}) },
		},
	}
}

// flagNameFromArg extracts the flag name from a single command-line token,
// stripping leading dashes and any "=value" suffix. It returns ("", false) for
// tokens that are not flags (e.g. positional arguments or a bare "-").
func flagNameFromArg(arg string) (string, bool) {
	if len(arg) < 2 || arg[0] != '-' {
		return "", false
	}

	name := strings.TrimLeft(arg, "-")
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}

	if name == "" {
		return "", false
	}

	return name, true
}

// hasFlag reports whether the given flag name appears anywhere in args.
func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if flagName, ok := flagNameFromArg(arg); ok && flagName == name {
			return true
		}
	}
	return false
}

// hasHelpFlag reports whether the operator requested help via --help or -h.
func hasHelpFlag(args []string) bool {
	return hasFlag(args, "help") || hasFlag(args, "h")
}

// detectMode returns the operational mode whose primary selector flag is
// present in args. Modes are checked in dispatch order, so the first matching
// selector wins. The boolean is false when no selector is present.
func detectMode(args []string) (operationalMode, bool) {
	for _, mode := range operationalModes() {
		if hasFlag(args, mode.selector) {
			return mode, true
		}
	}
	return operationalMode{}, false
}

// modeUsage returns the invocation summary and per-flag descriptions for a
// single mode.
func modeUsage(mode operationalMode) string {
	var b strings.Builder
	b.WriteString("Usage (")
	b.WriteString(mode.name)
	b.WriteString(" mode):\n  rewst_agent_config ")
	b.WriteString(mode.summary)
	b.WriteString("\n\nFlags:\n")
	b.WriteString(renderFlagDefaults(mode.flagSet()))
	return b.String()
}

// fullUsage returns the multi-line invocation summary followed by per-flag
// descriptions for every mode.
func fullUsage() string {
	modes := operationalModes()

	var b strings.Builder
	b.WriteString("Usage:\n")
	for _, mode := range modes {
		b.WriteString("  rewst_agent_config ")
		b.WriteString(mode.summary)
		b.WriteString("\n")
	}

	for _, mode := range modes {
		b.WriteString("\n")
		b.WriteString(capitalize(mode.name))
		b.WriteString(" mode:\n")
		b.WriteString(renderFlagDefaults(mode.flagSet()))
	}

	return b.String()
}

// capitalize upper-cases the first letter of a mode name for section headings.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// reportUsage renders the appropriate usage/help output for an invocation that
// did not match any mode and returns the process exit code.
//
//   - --help/-h prints the full multi-mode usage to stdout and exits 0.
//   - When a mode selector is present, the specific validation error for that
//     mode is printed to stderr followed by that mode's usage; exit code 1.
//   - When no selector is present, the full usage plus a hint to run --help is
//     printed to stderr; exit code 1.
//
// modeErrs maps each mode name to the error its context constructor returned.
func reportUsage(args []string, modeErrs map[string]error, stdout, stderr io.Writer) int {
	if hasHelpFlag(args) {
		_, _ = io.WriteString(stdout, fullUsage())
		return 0
	}

	if mode, ok := detectMode(args); ok {
		var b strings.Builder
		if err := modeErrs[mode.name]; err != nil {
			b.WriteString("error: ")
			b.WriteString(err.Error())
			b.WriteString("\n\n")
		}
		b.WriteString(modeUsage(mode))
		_, _ = io.WriteString(stderr, b.String())
		return 1
	}

	_, _ = io.WriteString(stderr, fullUsage()+"\nRun with --help for detailed usage.\n")
	return 1
}
