package interpreter

// processTree bundles the platform-specific hooks configureProcessGroup wires
// up so a command's full descendant tree — not just the immediate shell
// process — is torn down on cancellation. assign runs once cmd.Process is
// populated (i.e. after Start), for platforms that need post-start wiring to
// attach the child to whatever tracks its descendants (a Windows job
// object); release runs once the command has finished, unconditionally, to
// give back any OS resource configureProcessGroup allocated regardless of
// whether cancellation happened. Unix's implementation needs neither hook
// (its process-group kill is fully wired via SysProcAttr and cmd.Cancel), so
// both fields are left nil there and the zero value is a no-op.
type processTree struct {
	assign  func() error
	release func() error
}

func (p processTree) Assign() error {
	if p.assign == nil {
		return nil
	}
	return p.assign()
}

func (p processTree) Release() error {
	if p.release == nil {
		return nil
	}
	return p.release()
}
