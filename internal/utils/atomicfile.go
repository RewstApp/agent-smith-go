package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicTempSuffix is appended to the destination path to name the temporary
// file WriteFileAtomic writes before renaming it into place. Directory listings
// that enumerate committed records (the command journal, the postback spool)
// filter on their own suffixes and so never pick a temporary file up.
const AtomicTempSuffix = ".tmp"

// WriteFileAtomic writes data to path so that the file is never observable in
// a partially written state, and so that once the call returns the write has
// reached the storage device rather than only the page cache:
//
//  1. the bytes go to a temporary file alongside the destination;
//  2. that file is fsynced (Sync) so its contents are on disk;
//  3. it is renamed over the destination - a single atomic operation on every
//     platform the agent runs on;
//  4. the destination's parent directory is fsynced on platforms that support
//     it, so the rename itself is durable.
//
// Steps 1 and 3 alone are atomic against a crash of the agent process: a reader
// sees either the previous file or the complete new one. They are not atomic
// against a power loss or kernel panic, because neither the data nor the
// directory entry has been forced to the device - the rename could be on disk
// with the file's contents still in memory, or the rename could be lost. For a
// command journal entry that is the difference between an acknowledged command
// being replayed after the outage and being silently dropped as unreadable
// (sc-119835). Steps 2 and 4 close that gap, up to a filesystem that reports
// fsync complete before the data is actually durable; that is the one remaining
// exposure and is outside the agent's control.
//
// Windows has no directory fsync; the file Sync before the rename is the bound
// there, and NTFS journals the rename itself. A failure at any step leaves the
// destination as it was and removes the temporary file so a retry does not
// inherit it.
func WriteFileAtomic(fsys FileSystem, path string, data []byte, perm os.FileMode) error {
	tempPath := path + AtomicTempSuffix

	cleanup := func(err error) error {
		if removeErr := fsys.Remove(tempPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w (temporary file %s left behind: %v)", err, tempPath, removeErr)
		}
		return err
	}

	if err := fsys.WriteFile(tempPath, data, perm); err != nil {
		return cleanup(err)
	}
	if err := fsys.Sync(tempPath); err != nil {
		return cleanup(fmt.Errorf("sync %s: %w", tempPath, err))
	}
	if err := fsys.Rename(tempPath, path); err != nil {
		// Leave the destination as it was and take the half-written temp file
		// with us, so a retry does not inherit it.
		return cleanup(err)
	}
	if err := fsys.Sync(filepath.Dir(path)); err != nil {
		// The file is complete and in place; only the durability of the rename
		// is in doubt. Report it rather than hide it: the caller's acknowledgement
		// is supposed to mean durable.
		return fmt.Errorf("sync directory of %s: %w", path, err)
	}
	return nil
}
