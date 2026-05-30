package logmem

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultLimit = 100

type Entry struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type Recorder struct {
	mu      sync.Mutex
	limit   int
	entries []Entry
	seq     atomic.Uint64
}

func New(limit int) *Recorder {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Recorder{limit: limit}
}

func InstallDefault(limit int) *Recorder {
	recorder := New(limit)
	log.SetOutput(io.MultiWriter(os.Stderr, recorder))
	return recorder
}

func (r *Recorder) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(p, []byte{'\n'}) {
		msg := strings.TrimRight(string(line), "\r")
		if msg == "" {
			continue
		}
		r.add(msg)
	}
	return len(p), nil
}

func (r *Recorder) Snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]Entry, len(r.entries))
	copy(entries, r.entries)
	return entries
}

func (r *Recorder) add(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := Entry{Seq: r.seq.Add(1), Time: time.Now(), Message: message}
	if len(r.entries) == r.limit {
		copy(r.entries, r.entries[1:])
		r.entries[len(r.entries)-1] = entry
		return
	}
	r.entries = append(r.entries, entry)
}
