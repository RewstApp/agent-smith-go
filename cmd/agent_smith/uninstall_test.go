package main

import (
	"errors"
	"testing"
)

func newUninstallTestFS() *mockFileSystem {
	return &mockFileSystem{
		removeAllFunc: func(string) error { return nil },
	}
}

// ── early-exit tests (no sleep) ───────────────────────────────────────────────

func TestRunUninstall_OpenFails(t *testing.T) {
	params := &uninstallContext{
		OrgId: "test-org",
		ServiceManager: &mockServiceManager{
			openErr: errors.New("service not found"),
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

func TestRunUninstall_StopFails(t *testing.T) {
	params := &uninstallContext{
		OrgId: "test-org",
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: true, stopErr: errors.New("stop failed")},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

func TestRunUninstall_ActiveService_DeleteFails(t *testing.T) {
	params := &uninstallContext{
		OrgId: "test-org",
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: true, deleteErr: errors.New("delete failed")},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

func TestRunUninstall_InactiveService_DeleteFails(t *testing.T) {
	params := &uninstallContext{
		OrgId: "test-org",
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: false, deleteErr: errors.New("delete failed")},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}

// ── post-delete tests (run in parallel to share the serviceExecutableTimeout delay) ──

func TestRunUninstall_RemoveAllDataDirFails(t *testing.T) {
	t.Parallel()

	params := &uninstallContext{
		OrgId: "test-org",
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
		OrgId: "test-org",
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
		OrgId: "test-org",
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
		OrgId: "test-org",
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
		OrgId: "test-org",
		ServiceManager: &mockServiceManager{
			openService: &mockService{isActive: true},
		},
		FS: newUninstallTestFS(),
	}

	runUninstall(params)
}
