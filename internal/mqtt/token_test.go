package mqtt

import (
	"errors"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// fakeToken is a minimal paho Token whose completion is driven by the test.
// done is closed to resolve it; a token whose done is never closed models a
// broker that accepts a control packet and never acknowledges it.
type fakeToken struct {
	done chan struct{}
	err  error
}

func newFakeToken() *fakeToken { return &fakeToken{done: make(chan struct{})} }

func (t *fakeToken) resolve(err error) {
	t.err = err
	close(t.done)
}

func (t *fakeToken) Wait() bool {
	<-t.done
	return true
}

func (t *fakeToken) WaitTimeout(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.done:
		return true
	case <-timer.C:
		return false
	}
}

func (t *fakeToken) Done() <-chan struct{} { return t.done }
func (t *fakeToken) Error() error          { return t.err }

func TestWaitToken_CompletedBeforeTimeout(t *testing.T) {
	token := newFakeToken()
	go func() {
		time.Sleep(10 * time.Millisecond)
		token.resolve(nil)
	}()

	if got := WaitToken(token, 5*time.Second, nil); got != TokenCompleted {
		t.Errorf("expected TokenCompleted, got %v", got)
	}
}

func TestWaitToken_TimesOutOnUnresolvedToken(t *testing.T) {
	token := newFakeToken()
	defer token.resolve(nil)

	start := time.Now()
	if got := WaitToken(token, 50*time.Millisecond, nil); got != TokenTimedOut {
		t.Fatalf("expected TokenTimedOut, got %v", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("wait was not bounded by the timeout: took %v", elapsed)
	}
}

func TestWaitToken_InterruptedByStop(t *testing.T) {
	token := newFakeToken()
	defer token.resolve(nil)

	interrupt := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(interrupt)
	}()

	start := time.Now()
	// A long timeout, so returning promptly can only come from the interrupt.
	if got := WaitToken(token, time.Hour, interrupt); got != TokenInterrupted {
		t.Fatalf("expected TokenInterrupted, got %v", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("interrupt was not honored promptly: took %v", elapsed)
	}
}

// TestWaitToken_ResolvedTokenWinsOverPendingInterrupt pins the deterministic
// tie-break: when the token has already resolved and the interrupt is already
// pending, the result is the token's. Leaving both to one select would make a
// connection cycle racing a stop abandon completed work at random.
func TestWaitToken_ResolvedTokenWinsOverPendingInterrupt(t *testing.T) {
	token := newFakeToken()
	token.resolve(nil)

	interrupt := make(chan struct{})
	close(interrupt)

	for range 50 {
		if got := WaitToken(token, time.Hour, interrupt); got != TokenCompleted {
			t.Fatalf("expected a resolved token to win over a pending interrupt, got %v", got)
		}
	}
}

// TestWaitToken_ReportsCompletedOnTokenError confirms an operation that failed
// (rather than hung) is still reported as completed, leaving the error for the
// caller to inspect — the same split paho's Wait/Error pair has.
func TestWaitToken_ReportsCompletedOnTokenError(t *testing.T) {
	token := newFakeToken()
	token.resolve(errors.New("subscribe rejected"))

	if got := WaitToken(token, time.Second, nil); got != TokenCompleted {
		t.Fatalf("expected TokenCompleted for a resolved-with-error token, got %v", got)
	}
	if token.Error() == nil {
		t.Error("expected the token error to survive the wait")
	}
}

// publishStubClient is a paho Client that only implements Publish, returning the
// token the test supplies. Every other method panics so an unexpected call is
// loud rather than silently passing.
type publishStubClient struct {
	pahomqtt.Client
	token pahomqtt.Token
}

func (c *publishStubClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return c.token
}

func TestUpdateReportedProperties_BoundedByTimeout(t *testing.T) {
	token := newFakeToken()
	defer token.resolve(nil)

	start := time.Now()
	err := UpdateReportedProperties(
		&publishStubClient{token: token},
		ReportedProperties{AgentVersion: "1.2.3"},
		50*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected an error when the publish never resolves")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("publish wait was not bounded: took %v", elapsed)
	}
}

func TestUpdateReportedProperties_SucceedsOnFastAck(t *testing.T) {
	token := newFakeToken()
	token.resolve(nil)

	err := UpdateReportedProperties(
		&publishStubClient{token: token},
		ReportedProperties{AgentVersion: "1.2.3"},
		time.Second,
	)
	if err != nil {
		t.Fatalf("expected no error on a fast-acknowledged publish, got %v", err)
	}
}

func TestUpdateReportedProperties_SurfacesPublishError(t *testing.T) {
	token := newFakeToken()
	token.resolve(errors.New("not authorized"))

	err := UpdateReportedProperties(
		&publishStubClient{token: token},
		ReportedProperties{AgentVersion: "1.2.3"},
		time.Second,
	)
	if err == nil {
		t.Fatal("expected the publish error to be surfaced")
	}
}

// TestUpdateReportedProperties_NonPositiveTimeoutFallsBack confirms the wait can
// never be made unbounded by passing a zero or negative timeout.
func TestUpdateReportedProperties_NonPositiveTimeoutFallsBack(t *testing.T) {
	token := newFakeToken()
	token.resolve(nil)

	for _, timeout := range []time.Duration{0, -time.Second} {
		if err := UpdateReportedProperties(
			&publishStubClient{token: token},
			ReportedProperties{AgentVersion: "1.2.3"},
			timeout,
		); err != nil {
			t.Errorf("expected timeout %v to fall back to %v, got %v",
				timeout, utils.MqttPublishTimeout, err)
		}
	}
}
