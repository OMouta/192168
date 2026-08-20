package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// logLimit is how large the log may grow before it rolls over. One old file is
// kept, so it costs at most twice this on disk. A service can run for weeks and
// nobody can send a log that grew without limit.
const logLimit = 8 << 20

// LogFile is where the daemon writes when it has no console.
func LogFile(dataDir string) string {
	return filepath.Join(dataDir, "logs", "daemon.log")
}

// OpenLog opens the daemon's log for appending, rolling the previous one aside
// when it has grown too large.
func OpenLog(dataDir string) (io.WriteCloser, error) {
	path := LogFile(dataDir)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create the log directory: %w", err)
	}

	// The app writes its own log in here beside this one, as the signed-in
	// user, and this directory belongs to the service. Failing is not worth
	// stopping for: the daemon's own log still works, and there is nowhere to
	// report it to yet since this is what opens the log.
	_ = shareLogDir(directory)

	file, size, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &rollingLog{path: path, file: file, size: size}, nil
}

func openAppend(path string) (*os.File, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, fmt.Errorf("open the log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("measure the log: %w", err)
	}
	return file, info.Size(), nil
}

// rollingLog is an append-only log that starts a new file once the current one
// passes logLimit.
type rollingLog struct {
	path string

	mu   sync.Mutex
	file *os.File
	size int64
}

func (l *rollingLog) Write(record []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Roll before writing so a JSON record is never split across files.
	if l.size+int64(len(record)) > logLimit {
		if err := l.roll(); err != nil {
			return 0, err
		}
	}

	written, err := l.file.Write(record)
	l.size += int64(written)
	return written, err
}

// roll moves the current log aside and starts an empty one. Callers hold the
// lock.
func (l *rollingLog) roll() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close the log before rolling it: %w", err)
	}
	// Rename over the previous file, so one generation is kept.
	if err := os.Rename(l.path, l.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("roll the log: %w", err)
	}

	file, size, err := openAppend(l.path)
	if err != nil {
		return err
	}
	l.file, l.size = file, size
	return nil
}

func (l *rollingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
