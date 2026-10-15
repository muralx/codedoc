package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/muralx/codedoc/internal/xdg"
)

// maxLogSize is the size at which the log file is rotated to ".1".
const maxLogSize = 10 << 20

// Logger writes timestamped lines to stderr and to a log file that is
// renamed to <path>.1 once it would grow past 10 MB.
type Logger struct {
	mu     sync.Mutex
	stderr io.Writer
	path   string
	file   *os.File
	size   int64
	max    int64
}

// DefaultLogPath returns ${XDG_STATE_HOME:-~/.local/state}/codedoc/daemon.log;
// a relative XDG_STATE_HOME is ignored, as the XDG spec requires.
func DefaultLogPath() string {
	state, _ := xdg.StateDir()
	return filepath.Join(state, "daemon.log")
}

// NewLogger opens (appending) the log file at path, creating its
// directory. An empty path logs to stderr only.
func NewLogger(stderr io.Writer, path string) (*Logger, error) {
	l := &Logger{stderr: stderr, path: path, max: maxLogSize}
	if path == "" {
		return l, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return l, l.open()
}

func (l *Logger) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.file, l.size = f, info.Size()
	return nil
}

// Printf writes one line. File errors are reported on stderr only, so
// logging never stops the daemon.
func (l *Logger) Printf(format string, args ...any) {
	line := time.Now().Format(time.RFC3339) + " " + fmt.Sprintf(format, args...) + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.stderr, line)
	if l.file == nil {
		return
	}
	if l.size+int64(len(line)) > l.max && l.size > 0 {
		if err := l.rotate(); err != nil {
			_, _ = fmt.Fprintf(l.stderr, "log rotation failed: %v\n", err)
			return
		}
	}
	n, err := io.WriteString(l.file, line)
	l.size += int64(n)
	if err != nil {
		_, _ = fmt.Fprintf(l.stderr, "log write failed: %v\n", err)
	}
}

func (l *Logger) rotate() error {
	_ = l.file.Close()
	l.file = nil
	return errors.Join(os.Rename(l.path, l.path+".1"), l.open())
}

// Close closes the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
