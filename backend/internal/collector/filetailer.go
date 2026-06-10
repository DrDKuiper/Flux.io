package collector

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// FileTailer streams newly-appended lines from a file, similar to `tail -f`.
// It tolerates the file not existing yet (retrying until it appears) — useful
// when Suricata hasn't written its first eve.json line before Flux.io starts.
type FileTailer struct {
	path          string
	retryInterval time.Duration
	ready         chan struct{} // if non-nil, closed after open+seek; for test synchronisation
}

func NewFileTailer(path string) *FileTailer {
	return &FileTailer{path: path, retryInterval: 2 * time.Second}
}

// Lines starts tailing in a background goroutine and returns a channel of
// complete lines (without the trailing newline). It seeks to the end of the
// file on open, so only content written after Lines is called is delivered.
// The goroutine — and the channel — stop when ctx is cancelled.
func (t *FileTailer) Lines(ctx context.Context) <-chan string {
	out := make(chan string, 256)
	go t.run(ctx, out)
	return out
}

func (t *FileTailer) run(ctx context.Context, out chan<- string) {
	defer close(out)

	file := t.openWithRetry(ctx)
	if file == nil {
		return // context was cancelled while waiting for the file to appear
	}
	defer file.Close()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		log.Printf("filetailer: failed to seek to end of %s: %v", t.path, err)
		return
	}

	if t.ready != nil {
		close(t.ready)
		t.ready = nil
	}

	reader := bufio.NewReader(file)
	var pending strings.Builder

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if line != "" {
			pending.WriteString(line)
		}
		if err != nil {
			// No complete line yet — wait for more data
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

		// err == nil means ReadString found '\n' — we have a complete line
		trimmed := strings.TrimSuffix(strings.TrimSuffix(pending.String(), "\n"), "\r")
		pending.Reset()

		select {
		case <-ctx.Done():
			return
		case out <- trimmed:
		}
	}
}

// openWithRetry blocks (without busy-looping) until the file can be opened
// or ctx is cancelled, returning nil in the latter case.
func (t *FileTailer) openWithRetry(ctx context.Context) *os.File {
	for {
		file, err := os.Open(t.path)
		if err == nil {
			return file
		}
		log.Printf("filetailer: waiting for %s to exist (%v)", t.path, err)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(t.retryInterval):
		}
	}
}
