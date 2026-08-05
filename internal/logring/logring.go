// Package logring 提供线程安全的环形缓冲日志,供 GUI 展示最近日志。
package logring

import (
	"sync"
	"time"
)

// Entry 是一条日志。
type Entry struct {
	Time time.Time `json:"time"`
	Msg  string    `json:"msg"`
}

// Ring 是带容量上限的日志缓冲,实现 io.Writer。
type Ring struct {
	mu      sync.Mutex
	entries []Entry
	max     int
}

func New(max int) *Ring {
	if max <= 0 {
		max = 1000
	}
	return &Ring{max: max}
}

// Write 实现 io.Writer:按行切分并保留最近 max 条。
func (r *Ring) Write(p []byte) (int, error) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	start := 0
	for i, b := range p {
		if b == '\n' {
			if i > start {
				r.append(now, string(p[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(p) {
		r.append(now, string(p[start:]))
	}
	return len(p), nil
}

func (r *Ring) append(t time.Time, msg string) {
	if msg == "" {
		return
	}
	r.entries = append(r.entries, Entry{Time: t, Msg: msg})
	if len(r.entries) > r.max {
		r.entries = r.entries[len(r.entries)-r.max:]
	}
}

// Entries 返回最近的 limit 条日志(时间正序)。
func (r *Ring) Entries(limit int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.entries) {
		limit = len(r.entries)
	}
	out := make([]Entry, limit)
	copy(out, r.entries[len(r.entries)-limit:])
	return out
}

// Len 返回当前日志条数。
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
