//go:build windows

package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

func TestWindowsDefaultSystemInfoProvider_MACAddress_NoInterface(t *testing.T) {
	orig := netInterfaces
	netInterfaces = func() ([]net.Interface, error) { return nil, nil }
	defer func() { netInterfaces = orig }()

	sys := &windowsDefaultSystemInfoProvider{}
	_, err := sys.MACAddress()
	if !errors.Is(err, ErrNoMACAddress) {
		t.Errorf("expected ErrNoMACAddress, got %v", err)
	}
}

func TestWindowsDefaultSystemInfoProvider_MACAddress(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	result, err := sys.MACAddress()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil || len(*result) == 0 {
		t.Fatal("expected a valid mac address, got nil or empty")
	}

	if len(*result) != 12 {
		t.Errorf("expected 12-character mac, got %s", *result)
	}
}

func TestWindowsDefaultSystemInfoProvider_Hostname(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	expected, _ := os.Hostname()

	result, err := sys.Hostname()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if expected != result {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestWindowsDefaultSystemInfoProvider_HostPlatform(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	expected := "windows"

	result, err := sys.HostPlatform()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !strings.Contains(strings.ToLower(result), expected) {
		t.Errorf("expected to contain %s, got %s", expected, result)
	}
}

func TestWindowsDefaultSystemInfoProvider_CPUModelName(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	_, err := sys.CPUModelName()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultSystemInfoProvider_TotalMemoryBytes(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	result, err := sys.TotalMemoryBytes()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if result == 0 {
		t.Errorf("expected positive, got %v", result)
	}
}

func TestNewSystemInfoProvider(t *testing.T) {
	result := NewSystemInfoProvider()
	_, ok := result.(*windowsDefaultSystemInfoProvider)

	if !ok {
		t.Errorf("expected *windowsDefaultSystemInfoProvider, got %T", result)
	}
}

func TestWindowsDefaultDomainInfoProvider_ADDomain(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
	_, err := domain.ADDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDefaultPSRunner_TimesOutOnHungScript(t *testing.T) {
	orig := hostCommandTimeoutOverrideStr
	hostCommandTimeoutOverrideStr = "200ms"
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = orig })

	start := time.Now()
	done := make(chan struct{})
	var err error
	go func() {
		_, err = defaultPSRunner(context.Background(), "Start-Sleep -Seconds 30")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("defaultPSRunner did not return; the hung script was not killed by the timeout")
	}

	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("defaultPSRunner took %v to be killed; expected roughly 200ms", elapsed)
	}
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected error to mention timeout, got %q", err.Error())
	}
}

func TestWindowsDefaultDomainInfoProvider_ADDomain_CleanOutput(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "example.com", nil
		},
	}
	result, err := provider.ADDomain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || *result != "example.com" {
		t.Errorf("expected example.com, got %v", result)
	}
}

func TestWindowsDefaultDomainInfoProvider_ADDomain_EmptyOutput(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
	}
	result, err := provider.ADDomain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for workgroup machine, got %v", *result)
	}
}

// TestWindowsDefaultDomainInfoProvider_ADDomain_ProfileNoise documents the contamination
// bug: without -NoProfile, profile stdout prepends noise to the domain, so the returned
// value is the full noisy string instead of just the domain name.
func TestWindowsDefaultDomainInfoProvider_ADDomain_ProfileNoise(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			// Simulate profile output prepended before the actual domain (no -NoProfile).
			return "Welcome to PowerShell!\nexample.com", nil
		},
	}
	result, err := provider.ADDomain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a result, got nil")
	}
	// Without -NoProfile the full contaminated string is returned, not just the domain.
	if *result == "example.com" {
		t.Error(
			"profile noise corrupts domain value; -NoProfile suppresses this in production",
		)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
	_, err := domain.IsADDomainController(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController_True(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "True", nil
		},
	}
	result, err := provider.IsADDomainController(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected true, got false")
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController_False(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "False", nil
		},
	}
	result, err := provider.IsADDomainController(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected false, got true")
	}
}

// TestWindowsDefaultDomainInfoProvider_IsADDomainController_ProfileNoise documents that
// without -NoProfile, profile stdout causes the "True"/"False" comparison to fail, always
// returning false even for actual domain controllers.
func TestWindowsDefaultDomainInfoProvider_IsADDomainController_ProfileNoise(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			// Simulate profile noise before "True" (no -NoProfile).
			return "Welcome to PowerShell!\nTrue", nil
		},
	}
	result, err := provider.IsADDomainController(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Without -NoProfile the comparison fails and a DC is reported as non-DC.
	if result {
		t.Error(
			"profile noise suppresses DC true result; -NoProfile suppresses this in production",
		)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery:  defaultSCQuery,
	}
	_, err := domain.IsEntraConnectServer(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// hangingSCQuery stands in for "sc query" against a wedged Service Control
// Manager: it never returns on its own and only unblocks when the context the
// provider handed it is done. It records how many names were attempted so a
// test can tell an aborted check apart from one that plodded through all four.
func hangingSCQuery(attempts *int) scQueryFunc {
	return func(ctx context.Context, name string) error {
		*attempts++
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer_TimesOutOnHungQuery(t *testing.T) {
	orig := hostCommandTimeoutOverrideStr
	hostCommandTimeoutOverrideStr = "100ms"
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = orig })

	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery:  hangingSCQuery(&attempts),
	}

	start := time.Now()
	found, err := domain.IsEntraConnectServer(context.Background())
	elapsed := time.Since(start)

	// A hung query says nothing about whether the service exists, so the check
	// must surface an error rather than report the host as "not an Entra
	// Connect server".
	if err == nil {
		t.Fatal("expected an error when sc query hangs, got nil")
	}
	if found {
		t.Error("expected false alongside the error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected a timeout error, got %v", err)
	}

	// The four names share one budget: the first hang burns it and aborts the
	// whole check, instead of each name getting its own full timeout.
	if attempts != 1 {
		t.Errorf("expected the check to abort after 1 hung query, got %d attempts", attempts)
	}
	if elapsed > 2*time.Second {
		t.Errorf("IsEntraConnectServer took %v; expected the whole check to be bounded", elapsed)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer_SharesTimeoutBudget(t *testing.T) {
	orig := hostCommandTimeoutOverrideStr
	hostCommandTimeoutOverrideStr = "300ms"
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = orig })

	// Each query burns most of the budget before failing the way a
	// "service does not exist" query does. Without a shared deadline the four
	// names would take four times the timeout; with one, the check aborts part
	// way through.
	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery: func(ctx context.Context, name string) error {
			attempts++
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
			}
			return errors.New("the specified service does not exist")
		},
	}

	start := time.Now()
	_, err := domain.IsEntraConnectServer(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error once the shared budget is exhausted, got nil")
	}
	if attempts >= 4 {
		t.Errorf("expected the check to abort before all 4 names, got %d attempts", attempts)
	}
	if elapsed > 2*time.Second {
		t.Errorf("IsEntraConnectServer took %v; expected the whole check to be bounded", elapsed)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer_NotInstalled(t *testing.T) {
	// A fast "service does not exist" for every name is the normal result on a
	// host without Entra Connect: false, no error, every name tried.
	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery: func(ctx context.Context, name string) error {
			attempts++
			return errors.New("the specified service does not exist")
		},
	}

	found, err := domain.IsEntraConnectServer(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if found {
		t.Error("expected false when no Entra Connect service is installed")
	}
	if attempts != 4 {
		t.Errorf("expected all 4 service names to be queried, got %d", attempts)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer_Found(t *testing.T) {
	// The second name resolves, so the check stops there and reports true.
	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery: func(ctx context.Context, name string) error {
			attempts++
			if name == "Azure AD Sync" {
				return nil
			}
			return errors.New("the specified service does not exist")
		},
	}

	found, err := domain.IsEntraConnectServer(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !found {
		t.Error("expected true when an Entra Connect service is installed")
	}
	if attempts != 2 {
		t.Errorf("expected the check to stop at the matching name, got %d attempts", attempts)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer_CallerCanceled(t *testing.T) {
	// A canceled caller (the service shutting down mid get_installation) aborts
	// the check with an error rather than walking the remaining names and
	// reporting a value derived from failures it caused itself.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery:  hangingSCQuery(&attempts),
	}

	found, err := domain.IsEntraConnectServer(ctx)
	if err == nil {
		t.Fatal("expected an error when the caller's context is canceled, got nil")
	}
	if found {
		t.Error("expected false alongside the error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the cancellation to be reported, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected the check to abort after 1 query, got %d attempts", attempts)
	}
}

func TestNewHostInfo_BoundsHungEntraConnectQuery(t *testing.T) {
	// The context NewHostInfo hands the domain provider reaches the sc query
	// calls, so a wedged SCM cannot occupy the caller - a get_installation MQTT
	// worker, or a config/update CLI run - indefinitely. The field is reported
	// as false with a logged warning, and the rest of the host info still comes
	// back.
	orig := hostCommandTimeoutOverrideStr
	hostCommandTimeoutOverrideStr = "100ms"
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = orig })

	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: func(ctx context.Context, script string) (string, error) {
			return "", nil
		},
		scQuery: hangingSCQuery(&attempts),
	}

	start := time.Now()
	info, err := NewHostInfo(
		context.Background(),
		"test123",
		hclog.NewNullLogger(),
		&mockSystemInfoProvider{hostname: "mock", hostPlatform: "windows", cpuModelName: "fake"},
		domain,
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if info.IsEntraConnectServer {
		t.Error("expected false when the sc query never returns")
	}
	if attempts != 1 {
		t.Errorf("expected the check to abort after 1 hung query, got %d attempts", attempts)
	}
	if elapsed > 10*time.Second {
		t.Errorf("NewHostInfo took %v; expected the hung query to be bounded", elapsed)
	}
}

func TestNewHostInfo_PropagatesCallerCancellation(t *testing.T) {
	// A canceled caller context reaches the provider rather than being replaced
	// by a fresh background context somewhere in the chain.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	domain := &windowsDefaultDomainInfoProvider{
		psRunner: func(ctx context.Context, script string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return "", nil
		},
		scQuery: hangingSCQuery(&attempts),
	}

	info, err := NewHostInfo(
		ctx,
		"test123",
		hclog.NewNullLogger(),
		&mockSystemInfoProvider{hostname: "mock", hostPlatform: "windows", cpuModelName: "fake"},
		domain,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if info.IsEntraConnectServer {
		t.Error("expected false when the caller's context is already canceled")
	}
	if attempts != 1 {
		t.Errorf("expected the check to abort after 1 query, got %d attempts", attempts)
	}
}

func TestWindowsDefaultDomainInfoProvider_EntraDomain(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
	_, err := domain.EntraDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestResolveHostCommandTimeout(t *testing.T) {
	orig := hostCommandTimeoutOverrideStr
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = orig })

	hostCommandTimeoutOverrideStr = ""
	if got := resolveHostCommandTimeout(); got != hostCommandTimeout {
		t.Errorf("expected default %v, got %v", hostCommandTimeout, got)
	}

	hostCommandTimeoutOverrideStr = "5s"
	if got := resolveHostCommandTimeout(); got != 5*time.Second {
		t.Errorf("expected overridden 5s, got %v", got)
	}

	for _, bad := range []string{"not-a-duration", "-5s", "0s"} {
		hostCommandTimeoutOverrideStr = bad
		if got := resolveHostCommandTimeout(); got != hostCommandTimeout {
			t.Errorf("expected default for invalid override %q, got %v", bad, got)
		}
	}
}

func TestNewDomainInfoProvider(t *testing.T) {
	result := NewDomainInfoProvider()
	_, ok := result.(*windowsDefaultDomainInfoProvider)

	if !ok {
		t.Errorf("expected *windowsDefaultDomainInfoProvider, got %T", result)
	}
}
