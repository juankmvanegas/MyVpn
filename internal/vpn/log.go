package vpn

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// LogBuffer is a bounded in-memory log that also mirrors to the std logger.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

// NewLogBuffer creates a buffer holding up to max lines.
func NewLogBuffer(max int) *LogBuffer { return &LogBuffer{max: max} }

// Printf appends a formatted line.
func (b *LogBuffer) Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// never leak session cookies into logs
	if i := strings.Index(msg, "SVPNCOOKIE="); i >= 0 {
		msg = msg[:i] + "SVPNCOOKIE=<oculto>"
	}
	log.Print(msg)
	line := time.Now().Format("15:04:05") + "  " + msg
	b.mu.Lock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	b.mu.Unlock()
}

// Tail returns the last n lines.
func (b *LogBuffer) Tail(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.lines) {
		n = len(b.lines)
	}
	out := make([]string, n)
	copy(out, b.lines[len(b.lines)-n:])
	return out
}
