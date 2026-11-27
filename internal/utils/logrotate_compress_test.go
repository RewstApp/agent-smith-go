package utils

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// log_compress (sc-132996): rotated copies become path.N.gz off the writer's
// path, the retention bound holds across both forms, and a failure keeps the
// plain copy with one WARN.

func newCompressingRotator(t *testing.T, maxBytes int64, maxFiles int) (*RotatingFile, string) {
	t.Helper()
	r, path := newTestRotator(t, maxBytes, maxFiles)
	r.compressRetryDelay = time.Millisecond
	r.SetCompress(true)
	t.Cleanup(r.waitCompression)
	return r, path
}

func gunzip(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s is not gzip: %v", path, err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	return string(b)
}

// rotatedFiles lists the names beside the active file other than itself.
func rotatedFiles(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestRotatingFile_CompressGzipsEveryRotatedCopyAndKeepsTheBound(t *testing.T) {
	const maxFiles = 3
	r, path := newCompressingRotator(t, 4, maxFiles)

	for i := 0; i < maxFiles+3; i++ {
		mustWrite(t, r, fmt.Sprintf("L%d\n", i)) // every line after the first rotates
		r.waitCompression()
	}

	want := []string{"agent.log.1.gz", "agent.log.2.gz", "agent.log.3.gz"}
	if got := rotatedFiles(t, path); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("beside the log: %v, want %v", got, want)
	}
	for i := 1; i <= maxFiles; i++ {
		got := gunzip(t, fmt.Sprintf("%s.%d.gz", path, i))
		if got != fmt.Sprintf("L%d\n", maxFiles+2-i) {
			t.Errorf(".%d.gz = %q", i, got)
		}
	}
	if got := readFile(t, path); got != fmt.Sprintf("L%d\n", maxFiles+2) {
		t.Errorf("active = %q, want only the last line and no note", got)
	}
}

// Rotations that outrun the compressor - the copy moves to another slot while
// it is being gzipped - still end with every copy compressed exactly once and
// nothing lost or duplicated.
func TestRotatingFile_CompressCatchesUpWithRapidRotations(t *testing.T) {
	const maxFiles = 4
	r, path := newCompressingRotator(t, 4, maxFiles)
	for i := 0; i < 50; i++ {
		mustWrite(t, r, fmt.Sprintf("%02d\n", i))
	}
	r.waitCompression()

	got := rotatedFiles(t, path)
	if len(got) != maxFiles {
		t.Fatalf("beside the log: %v, want %d compressed copies", got, maxFiles)
	}
	for i := 1; i <= maxFiles; i++ {
		if c := gunzip(t, fmt.Sprintf("%s.%d.gz", path, i)); c != fmt.Sprintf("%02d\n", 49-i) {
			t.Errorf(".%d.gz = %q", i, c)
		}
	}
}

// Turning compression on compresses the plain copies already there; turning
// it off leaves new rotations plain while the compressed copies keep shifting
// and ageing out with the rest.
func TestRotatingFile_SetCompressAppliesAtOnce(t *testing.T) {
	r, path := newTestRotator(t, 2, 3)
	r.compressRetryDelay = time.Millisecond
	t.Cleanup(r.waitCompression)
	mustWrite(t, r, "a\n")
	mustWrite(t, r, "b\n") // .1 = a, plain
	if !exists(path + ".1") {
		t.Fatal("no plain copy before compression is on")
	}

	r.SetCompress(true)
	r.waitCompression()
	if exists(path+".1") || gunzip(t, path+".1.gz") != "a\n" {
		t.Fatalf("the existing copy was not compressed: %v", rotatedFiles(t, path))
	}

	r.SetCompress(false)
	mustWrite(t, r, "c\n") // .1 = b plain, .2.gz = a
	r.waitCompression()
	if readFile(t, path+".1") != "b\n" || gunzip(t, path+".2.gz") != "a\n" {
		t.Fatalf("after turning it off: %v", rotatedFiles(t, path))
	}
	mustWrite(t, r, "d\n") // .1 = c, .2 = b, .3.gz = a
	mustWrite(t, r, "e\n") // a ages out in its compressed form
	if exists(path+".3.gz") && gunzip(t, path+".3.gz") == "a\n" {
		t.Error("the compressed copy did not age out")
	}
	if exists(path + ".4.gz") {
		t.Error("a compressed copy outlived the retention bound")
	}
}

// A copy that cannot be compressed stays plain, the failure is written into
// the log once (not once per copy), and the recovery once.
func TestRotatingFile_CompressFailureKeepsThePlainCopyAndWarnsOnce(t *testing.T) {
	r, path := newTestRotator(t, 2, 5)
	r.compressRetryDelay = time.Millisecond
	t.Cleanup(r.waitCompression)
	failing := true
	r.gzipFile = func(src, dst string) error {
		if failing {
			_ = os.WriteFile(dst, []byte("partial"), 0o600)
			return errors.New("disk full")
		}
		return gzipFile(src, dst)
	}
	r.SetCompress(true)
	mustWrite(t, r, "a\n")
	mustWrite(t, r, "b\n")
	r.waitCompression()
	mustWrite(t, r, "c\n")
	r.waitCompression()
	mustWrite(t, r, "d\n") // flushes the owed note at a line boundary

	if !exists(path+".1") || !exists(path+".2") {
		t.Fatalf("the plain copies were not kept: %v", rotatedFiles(t, path))
	}
	for _, name := range rotatedFiles(t, path) {
		if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".tmp") {
			t.Errorf("a failed compression left %s", name)
		}
	}
	var all bytes.Buffer
	for _, p := range []string{path, path + ".1", path + ".2", path + ".3"} {
		if b, err := os.ReadFile(p); err == nil {
			all.Write(b)
		}
	}
	if n := strings.Count(all.String(), "log compression failed"); n != 1 {
		t.Errorf("%d failure notes, want 1:\n%s", n, all.String())
	}
	if !strings.Contains(all.String(), "disk full") {
		t.Errorf("the note does not say why:\n%s", all.String())
	}

	failing = false
	r.SetCompress(true) // a later rotation or this retries every plain copy
	r.waitCompression()
	mustWrite(t, r, "e\n")
	r.waitCompression()
	for _, name := range rotatedFiles(t, path) {
		if !strings.HasSuffix(name, ".gz") {
			t.Errorf("%s still plain after recovery", name)
		}
	}
	if !strings.Contains(readFile(t, path)+gunzip(t, path+".1.gz"), "log compression recovered") {
		t.Error("the recovery was not recorded")
	}
}

// A compression temp file left by a process that exited mid-gzip is removed
// by the next pass, and never mistaken for a rotated copy.
func TestRotatingFile_CompressRemovesAStaleTempFile(t *testing.T) {
	r, path := newTestRotator(t, 2, 3)
	r.compressRetryDelay = time.Millisecond
	t.Cleanup(r.waitCompression)
	if err := os.WriteFile(path+".1.gz.tmp", []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.SetCompress(true)
	mustWrite(t, r, "a\n")
	mustWrite(t, r, "b\n")
	r.waitCompression()
	if got := rotatedFiles(t, path); strings.Join(got, ",") != "agent.log.1.gz" {
		t.Errorf("beside the log: %v", got)
	}
}

// Off (the default) nothing changes: the copies are plain and no worker runs.
func TestRotatingFile_CompressOffLeavesCopiesPlain(t *testing.T) {
	r, path := newTestRotator(t, 2, 3)
	mustWrite(t, r, "a\n")
	mustWrite(t, r, "b\n")
	r.waitCompression()
	if got := rotatedFiles(t, path); strings.Join(got, ",") != "agent.log.1" {
		t.Errorf("beside the log: %v", got)
	}
}
