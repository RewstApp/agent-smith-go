//go:build windows

package utils

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION. Windows maps a running image
// to it: the loader keeps the executable open denying write sharing, so opening
// it for writing fails with this error for exactly as long as the process lives.
// It is the same error that made an in-place update fail after the fixed five
// second sleep, used here as the signal to wait on instead of the symptom.
const errSharingViolation = syscall.Errno(32)

// executableInUse probes whether name is currently running. A successful open
// means no process holds the image and the file can be replaced; a sharing
// violation means the old process is still alive.
//
// The file is opened without O_TRUNC and closed immediately, so a successful
// probe leaves its contents untouched.
func executableInUse(name string) (bool, error) {
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err == nil {
		return false, file.Close()
	}

	if errors.Is(err, os.ErrNotExist) {
		// Nothing installed at that path yet: no process can be holding it.
		return false, nil
	}

	if errors.Is(err, errSharingViolation) {
		return true, nil
	}

	return false, err
}

// EnsureSecureDir creates path if it does not exist. Windows has no POSIX
// mode/ownership bits to re-assert here, so an existing entry is only checked
// for being a plain directory; a symlink or non-directory entry at path is
// refused rather than followed or replaced, since MkdirAll never produces
// one. This is deliberately the only check on Windows: the command scripts
// directory (agent.GetScriptsDirectory) stays at its historical location on
// this platform rather than moving under the agent-owned data directory the
// way it did on Linux/macOS — see the comment on that function for why — so
// there is no ProgramData-inherited ACL to lean on here the way there would
// be if it had moved.
func EnsureSecureDir(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, SecureDirMode); err != nil {
			return fmt.Errorf("failed to create secure directory %s: %w", path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("refusing to use %s: exists and is not a plain directory", path)
	}

	return nil
}

// EnsureSecureFile removes the broad-access grants (Everyone / Authenticated
// Users / Users) from path's ACL, the file-level counterpart to
// SecureDataDirectoryACL. It exists for the device config file specifically
// (sc-108849): ProgramData's default ACL grants ordinary authenticated
// Users read access, which is exactly the exposure this closes, and an
// installation that pre-dates this hardening needs its already-written
// config.json corrected in place, not just files written after the fix.
//
// A path that does not exist is not an error — nothing has been written yet.
// A symlink or reparse point is refused rather than followed, since
// icacls operates on whatever the link resolves to.
func EnsureSecureFile(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 || fi.IsDir() {
		return fmt.Errorf("refusing to use %s: exists and is not a plain file", path)
	}

	return applyOwnerOnlyACL(path, "", false)
}

// windowsBroadAccessSIDs are the well-known SIDs whose default inheritance
// from ProgramData is what makes a fresh directory or file readable by any
// local account: Everyone, Authenticated Users, and the built-in Users
// group. Well-known SIDs are used instead of the localized group names so
// this works regardless of the OS display language.
var windowsBroadAccessSIDs = []string{
	"*S-1-1-0",      // Everyone
	"*S-1-5-11",     // Authenticated Users
	"*S-1-5-32-545", // BUILTIN\Users
}

// applyOwnerOnlyACL locks path down by removing the specific broad-access
// grants (Everyone / Authenticated Users / Users) that ProgramData's default
// ACL inherits onto everything created under it, rather than replacing the
// ACL outright with a hand-picked allow-list.
//
// This is deliberately a subtraction, not a replacement: SYSTEM,
// Administrators, and — critically — the file's owner (via the CREATOR
// OWNER inherited entry, which NTFS already resolves to whichever account
// actually created the object) are left exactly as ProgramData's default
// already grants them. An earlier version of this function replaced the
// whole ACL with an explicitly enumerated allow-list (SYSTEM, Administrators,
// and whichever account os/user.Current() resolved to); on the GitHub
// Actions Windows runner that left config.json unreadable even to a
// same-job, same-account step run moments later, for reasons that could not
// be root-caused without a real Windows box to instrument. Only removing the
// specific over-broad grants avoids needing to correctly guess "who must
// still be able to read this" at all.
//
// extraAccount, when non-empty, is granted Full Control additively (plain
// /grant, not /grant:r) on top of the subtraction — this is how
// ServiceUsername is honored when the account the service will run as
// differs from whoever performed the install/update and so would not
// otherwise inherit owner access.
//
// /inheritance:d converts inherited entries (including the ones the removal
// step targets) to explicit ones without changing effective access, which
// icacls requires before individual entries can be removed. When recursive
// is true, /T /C additionally reapplies both the conversion and the removal
// to every file already inside the directory (config.json, the log file,
// spooled postbacks) so an installation from before this hardening is
// corrected in place; /C lets the recursive pass continue past a single file
// it cannot touch (e.g. one held open) instead of aborting the whole
// operation.
//
// icacls is a stock component of every supported Windows release, so
// shelling out to it (rather than hand-rolling security-descriptor
// construction) keeps this both readable and consistent with the well-known
// SIDs used above.
func applyOwnerOnlyACL(path string, extraAccount string, recursive bool) error {
	recurseArgs := []string{}
	if recursive {
		recurseArgs = []string{"/T", "/C"}
	}

	run := func(args ...string) error {
		cmd := exec.Command("icacls", args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf(
				"icacls %v failed for %s: %w (output: %s)",
				args, path, err, strings.TrimSpace(string(output)),
			)
		}
		return nil
	}

	convertArgs := append([]string{path, "/inheritance:d"}, recurseArgs...)
	if err := run(convertArgs...); err != nil {
		return err
	}

	removeArgs := append([]string{path, "/remove:g"}, windowsBroadAccessSIDs...)
	removeArgs = append(removeArgs, recurseArgs...)
	if err := run(removeArgs...); err != nil {
		return err
	}

	if extraAccount != "" {
		perm := "F"
		if recursive {
			perm = "(OI)(CI)F"
		}
		grantArgs := append([]string{path, "/grant", extraAccount + ":" + perm}, recurseArgs...)
		if err := run(grantArgs...); err != nil {
			return err
		}
	}

	return nil
}

// SecureDataDirectoryACL restricts path — the org's data directory, which
// holds config.json and its Azure IoT Hub SharedAccessKey and GitHub token
// (sc-108849) — by removing the Everyone / Authenticated Users / Users
// grants ProgramData's default ACL inherits onto it, recursively correcting
// files already inside (config.json, the log file, spooled postbacks). SYSTEM,
// Administrators, and the directory's owner are left untouched. Pass the
// configured service account (ServiceUsername) as extraAccount at
// install/update time so a service that runs as a different account than the
// one performing the install/update — and so would not otherwise inherit
// owner access — is not locked out of its own config file; service startup
// itself needs no extraAccount, since owner access already covers whichever
// account it runs as by then.
//
// This is deliberately separate from EnsureSecureDir: that function is also
// used for the command scripts directory (agent.GetScriptsDirectory), and
// leaving its Windows behaviour untouched keeps this credential-exposure fix
// from carrying any risk of regressing command execution.
//
// Failure is returned to the caller rather than treated as fatal here:
// callers use this as a best-effort migration step so a transient icacls
// failure cannot take an otherwise-healthy agent down.
func SecureDataDirectoryACL(path string, extraAccount string) error {
	return applyOwnerOnlyACL(path, extraAccount, true)
}
