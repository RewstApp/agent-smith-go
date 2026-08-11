package main

import (
	"errors"
	"testing"
	"time"
)

func newUninstallTestFS() *mockFileSystem {
	return &mockFileSystem{
		removeAllFunc: func(string) error { return nil },
	}
}

// ── early-exit tests (no sleep) ───────────────────────────────────────────────

func TestRunUninstall_OpenFails(t *testing.T) {
	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openErr: errors.New("service not found"),
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

func TestRunUninstall_StopFails(t *testing.T) {
	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: true, stopErr: errors.New("stop failed")},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

// A stop that fails — a wedged service that never reaches Stopped — must abort
// the uninstall before anything is removed, so the endpoint is left either fully
// installed or fully removed, never mixed.
func TestRunUninstall_StopFails_RemovesNothing(t *testing.T) {
	var removed []string
	svc := &mockService{
		isActive: true,
		stopErr: errors.New(
			"service rewst_agent_smith_test-org did not stop within 5m0s: " +
				"last observed state StopPending",
		),
	}
	params := &uninstallContext{
		OrgId:          "test-org",
		exitWait:       stubExitWait(),
		ServiceManager: &mockServiceManager{openService: svc},
		FS: &mockFileSystem{
			removeAllFunc: func(path string) error {
				removed = append(removed, path)
				return nil
			},
		},
	}

	runUninstall(params)

	if !svc.stopCalled {
		t.Error("expected Stop to be attempted")
	}
	if svc.deleteCalled {
		t.Error("expected the service registration to survive a failed stop")
	}
	if len(removed) != 0 {
		t.Errorf("expected no directories removed after a failed stop, got %v", removed)
	}
}

func TestRunUninstall_ActiveService_DeleteFails(t *testing.T) {
	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: true, deleteErr: errors.New("delete failed")},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

func TestRunUninstall_InactiveService_DeleteFails(t *testing.T) {
	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: false, deleteErr: errors.New("delete failed")},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

// A process that never exits must abort the uninstall before anything is
// removed: deleting files out from under a live process is what leaves a
// half-removed installation that neither runs nor reinstalls.
func TestRunUninstall_ProcessNeverExits_RemovesNothing(t *testing.T) {
	clock := newFakeClock()
	var removed []string
	svc := &mockService{isActive: true}
	params := &uninstallContext{
		OrgId:          "test-org",
		ServiceManager: &mockServiceManager{openService: svc},
		exitWait:       clock.options(2*time.Minute, 250*time.Millisecond),
		FS: &mockFileSystem{
			removeAllFunc: func(path string) error {
				removed = append(removed, path)
				return nil
			},
			// The old process holds its image open for as long as it lives.
			executableInUseFunc: func(string) (bool, error) { return true, nil },
		},
	}

	runUninstall(params)

	if !svc.stopCalled {
		t.Error("expected Stop to be attempted")
	}
	if svc.deleteCalled {
		t.Error("expected the service registration to survive an aborted uninstall")
	}
	if len(removed) != 0 {
		t.Errorf("expected no directories removed while the process is alive, got %v", removed)
	}
	if clock.slept < 2*time.Minute {
		t.Errorf("expected the full deadline to be waited out, waited %s", clock.slept)
	}
}

// A slow but healthy shutdown must be waited out and then removed completely —
// nothing is deleted until the process is observably gone.
func TestRunUninstall_WaitsForExitBeforeRemoving(t *testing.T) {
	clock := newFakeClock()
	probes := 0
	var removed []string
	probesWhenFirstRemoved := -1
	svc := &mockService{isActive: true}
	params := &uninstallContext{
		OrgId:          "test-org",
		ServiceManager: &mockServiceManager{openService: svc},
		exitWait:       clock.options(2*time.Minute, 250*time.Millisecond),
		FS: &mockFileSystem{
			removeAllFunc: func(path string) error {
				if probesWhenFirstRemoved == -1 {
					probesWhenFirstRemoved = probes
				}
				removed = append(removed, path)
				return nil
			},
			executableInUseFunc: func(string) (bool, error) {
				probes++
				// The image is released on the third observation.
				return probes < 3, nil
			},
		},
	}

	runUninstall(params)

	if probesWhenFirstRemoved != 3 {
		t.Errorf(
			"expected removal to start only once the process was gone, started after %d probes",
			probesWhenFirstRemoved,
		)
	}
	if !svc.deleteCalled {
		t.Error("expected the service registration to be deleted")
	}
	if len(removed) != 3 {
		t.Errorf("expected the three installation directories to be removed, got %v", removed)
	}
	if clock.sleeps != 2 {
		t.Errorf("expected 2 polls while the process was shutting down, got %d", clock.sleeps)
	}
}

// ── post-delete tests ─────────────────────────────────────────────────────────

func TestRunUninstall_RemoveAllDataDirFails(t *testing.T) {
	t.Parallel()

	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: false},
		},
		FS: &mockFileSystem{
			removeAllFunc: func(string) error { return errors.New("remove failed") },
		},
	}

	runUninstall(params)
}

func TestRunUninstall_RemoveAllProgramDirFails(t *testing.T) {
	t.Parallel()

	call := 0
	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: false},
		},
		FS: &mockFileSystem{
			removeAllFunc: func(string) error {
				call++
				if call == 2 {
					return errors.New("remove failed")
				}
				return nil
			},
		},
	}

	runUninstall(params)
}

func TestRunUninstall_RemoveAllScriptsDirFails(t *testing.T) {
	t.Parallel()

	call := 0
	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: false},
		},
		FS: &mockFileSystem{
			removeAllFunc: func(string) error {
				call++
				if call == 3 {
					return errors.New("remove failed")
				}
				return nil
			},
		},
	}

	runUninstall(params)
}

func TestRunUninstall_Success(t *testing.T) {
	t.Parallel()

	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: false},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

func TestRunUninstall_ActiveService_Success(t *testing.T) {
	t.Parallel()

	params := &uninstallContext{
		OrgId:    "test-org",
		exitWait: stubExitWait(),
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: true},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}
