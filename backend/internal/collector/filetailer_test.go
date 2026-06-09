package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileTailer_StreamsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eve.json")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if _, err := f.WriteString("line-written-before-tailing-starts\n"); err != nil {
		t.Fatalf("failed to seed file: %v", err)
	}
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewFileTailer(path)
	lines, err := tailer.Lines(ctx)
	if err != nil {
		t.Fatalf("Lines returned error: %v", err)
	}

	// Give the tailer a moment to seek to EOF before we append —
	// it must NOT replay the line written before it started.
	time.Sleep(100 * time.Millisecond)

	appendFile, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to reopen file for append: %v", err)
	}
	if _, err := appendFile.WriteString("line-appended-after-tailing-starts\n"); err != nil {
		t.Fatalf("failed to append: %v", err)
	}
	appendFile.Close()

	select {
	case line := <-lines:
		if line != "line-appended-after-tailing-starts" {
			t.Fatalf("expected the appended line, got %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for appended line")
	}
}

func TestFileTailer_RetriesUntilFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-yet-created.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewFileTailer(path)
	tailer.retryInterval = 50 * time.Millisecond // speed up retries in tests

	lines, err := tailer.Lines(ctx)
	if err != nil {
		t.Fatalf("Lines returned error: %v", err)
	}

	// The goroutine is now retrying every 50ms. Wait briefly to confirm it's
	// retrying (file still absent), then create the file empty so the goroutine
	// opens it and seeks to EOF at offset 0.
	time.Sleep(75 * time.Millisecond)

	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Wait for the goroutine to open the now-existing empty file and seek to
	// EOF (offset 0). Two retry windows (100ms) is a generous margin.
	time.Sleep(150 * time.Millisecond)

	appendFile, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	if _, err := appendFile.WriteString("first-line\n"); err != nil {
		t.Fatalf("failed to append first line: %v", err)
	}
	appendFile.Close()

	select {
	case line := <-lines:
		if line != "first-line" {
			t.Fatalf("expected %q, got %q", "first-line", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for line from a file created after tailing started")
	}
}
