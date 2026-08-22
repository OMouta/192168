package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// logLimit is how large the daemon log may grow before it rolls over. One old
// file is kept, so it costs at most twice this on disk. A service can run for
// weeks and nobody can send a log that grew without limit.
const logLimit = 8 << 20

// LogFile is where the daemon writes when it has no console.
func LogFile(dataDir string) string {
	return filepath.Join(dataDir, "logs", "daemon.log")
}

// PacketLogFile is where the per-packet detail goes when it is turned on. It is
// beside the daemon's log rather than inside it because it is written orders of
// magnitude faster, and mixing the two would roll the history worth reading out
// of the file within seconds.
func PacketLogFile(dataDir string) string {
	return filepath.Join(dataDir, "logs", "packets.log")
}

// OpenLog opens the daemon's log for appending, rolling the previous one aside
// when it has grown too large.
func OpenLog(dataDir string) (*RollingLog, error) {
	path := LogFile(dataDir)
	if err := prepareLogDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return OpenRolling(path, logLimit)
}

// prepareLogDir creates the log directory and opens it to the signed-in user.
//
// The app writes its own log in here beside the daemon's, as the signed-in
// user, and this directory belongs to the service. Failing to widen it is not
// worth stopping for: the daemon's own log still works, and there is nowhere to
// report it to yet since this is what opens the log.
func prepareLogDir(directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create the log directory: %w", err)
	}
	_ = shareLogDir(directory)
	return nil
}

// OpenRolling opens an append-only log that starts a new file once the current
// one passes limit.
func OpenRolling(path string, limit int64) (*RollingLog, error) {
	if err := prepareLogDir(filepath.Dir(path)); err != nil {
		return nil, err
	}

	file, size, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &RollingLog{path: path, limit: limit, file: file, size: size}, nil
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

// RollingLog is an append-only log that starts a new file once the current one
// passes its limit.
type RollingLog struct {
	path  string
	limit int64

	mu   sync.Mutex
	file *os.File
	size int64
}

var _ io.WriteCloser = (*RollingLog)(nil)

func (l *RollingLog) Write(record []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Roll before writing so a JSON record is never split across files.
	if l.size+int64(len(record)) > l.limit {
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
func (l *RollingLog) roll() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close the log before rolling it: %w", err)
	}
	// Rename over the previous file, so one generation is kept.
	if err := os.Rename(l.path, l.previous()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("roll the log: %w", err)
	}

	file, size, err := openAppend(l.path)
	if err != nil {
		return err
	}
	l.file, l.size = file, size
	return nil
}

// previous is the one generation kept behind the current file.
func (l *RollingLog) previous() string { return l.path + ".1" }

// Clear empties the log and drops the generation behind it.
//
// It has to happen here rather than in whoever asked, because this file is held
// open. Deleting it from outside appears to work and does not: Windows keeps a
// deleted file alive behind an open handle, so the daemon would carry on
// writing into something nobody can read, and the size this tracks for the
// rollover would be wrong until the next restart.
func (l *RollingLog) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Closing and starting again rather than truncating in place. This handle
	// is append-only, and Windows will not let an append-only handle shorten
	// the file it is on: it comes back as access denied, on a file this process
	// owns and is writing to.
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close the log before emptying it: %w", err)
	}

	removed := removeIfPresent(l.path)
	if err := removeIfPresent(l.previous()); removed == nil {
		removed = err
	}

	// Reopened whatever happened above. A log that could not be deleted is
	// worth reporting, but leaving the daemon with no log at all over it is
	// not: everything after this point would go nowhere.
	file, size, err := openAppend(l.path)
	if err != nil {
		return err
	}
	l.file, l.size = file, size

	if removed != nil {
		return fmt.Errorf("empty the log: %w", removed)
	}
	return nil
}

func (l *RollingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// RemoveLog deletes a log and the generation behind it, treating one that was
// never written as done.
func RemoveLog(path string) error {
	if err := removeIfPresent(path); err != nil {
		return err
	}
	return removeIfPresent(path + ".1")
}

// removeIfPresent deletes a file, treating one that was never there as done.
func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
