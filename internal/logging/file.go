// Package logging provides bounded, synchronous file logging for one process.
package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const DefaultDir = "/app/logs"
const MaxBytes int64 = 50 << 20
const Backups = 5

type File struct {
	mu          sync.Mutex
	path        string
	file        *os.File
	size, limit int64
	backups     int
	closed      bool
}

func Open(dir string) (*File, error) { return open(dir, MaxBytes, Backups) }
func open(dir string, limit int64, backups int) (*File, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	f := &File{path: filepath.Join(dir, "xuandu.log"), limit: limit, backups: backups}
	return f, f.reopen()
}
func (f *File) reopen() error {
	file, err := os.OpenFile(f.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	f.file, f.size = file, info.Size()
	return nil
}
func (f *File) rotate() error {
	err := f.file.Close()
	f.file = nil
	if err != nil {
		return err
	}
	for n := f.backups; n > 1; n-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", f.path, n-1), fmt.Sprintf("%s.%d", f.path, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(f.path, f.path+".1"); err != nil {
		return err
	}
	return f.reopen()
}
func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, os.ErrClosed
	}
	// Keep a single entry intact; reject oversized entries instead of unbounding disk use.
	if int64(len(p)) > f.limit {
		return 0, fmt.Errorf("log entry exceeds file limit")
	}
	if f.file == nil {
		if err := f.reopen(); err != nil {
			return 0, err
		}
	}
	if f.size+int64(len(p)) > f.limit {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	return n, err
}
func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.file == nil {
		return nil
	}
	return f.file.Close()
}

// Tee always writes to stdout, even when the disk is full or unavailable.
// Report file failures explicitly because slog does not return handler errors.
func Tee(stdout, file, stderr io.Writer) io.Writer {
	return &tee{stdout: stdout, file: file, stderr: stderr}
}

type tee struct {
	mu                   sync.Mutex
	stdout, file, stderr io.Writer
}

func (t *tee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.stdout.Write(p)
	if _, fileErr := t.file.Write(p); fileErr != nil {
		fmt.Fprintf(t.stderr, "xuandu: file log write failed: %v\n", fileErr)
	}
	return n, err
}
