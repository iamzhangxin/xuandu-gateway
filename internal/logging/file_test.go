package logging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotationAndRestart(t *testing.T) {
	dir := t.TempDir()
	f, err := open(dir, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"1111", "2222", "3333", "4444", "5555", "6666", "7777"} {
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	for file, want := range map[string]string{"xuandu.log": "7777", "xuandu.log.1": "55556666", "xuandu.log.2": "33334444"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q, %v", file, data, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatal("unbounded rotation")
	}
	f, err = open(dir, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Write([]byte("8888")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "xuandu.log"))
	if string(data) != "77778888" {
		t.Fatal("restart truncated existing logs")
	}
	if _, err = f.Write([]byte("oversized")); err == nil {
		t.Fatal("oversized entry accepted")
	}
	f.Close()
	if _, err = f.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}
func TestConcurrentWritesAndDiskFailure(t *testing.T) {
	dir := t.TempDir()
	f, err := open(dir, 1<<20, 2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Go(func() {
			for i := 0; i < 50; i++ {
				if _, err := fmt.Fprintln(f, "complete log entry"); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	f.Close()
	data, _ := os.ReadFile(filepath.Join(dir, "xuandu.log"))
	if strings.Count(string(data), "complete log entry\n") != 1000 {
		t.Fatal("entries lost or interleaved")
	}
	var stdout, stderr bytes.Buffer
	writer := Tee(&stdout, f, &stderr)
	if _, err := writer.Write([]byte("still visible")); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "still visible" || !strings.Contains(stderr.String(), "file log write failed") {
		t.Fatal("file failure hid stdout or diagnostic")
	}
}
