// SPDX-License-Identifier: GPL-3.0-or-later

// Package history appends field-change records to a rotating JSONL file and
// keeps the most recent ones in memory.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sync"
	"time"
)

const (
	DefaultMaxBytes = 50 << 20
	keep            = 1000
)

type Record struct {
	T        time.Time `json:"t"`
	Unit     string    `json:"unit"`
	Endpoint string    `json:"endpoint"`
	Path     string    `json:"path"`
	Old      string    `json:"old"`
	New      string    `json:"new"`
	Source   string    `json:"source,omitempty"`
}

type Log struct {
	mu     sync.Mutex
	path   string
	max    int64
	f      *os.File
	size   int64
	recent []Record // oldest first, at most keep
	subs   []func(Record)
}

// Open loads the last records from path (and path.1 if needed) and opens path for appending.
func Open(path string, maxBytes int64) (*Log, error) {
	l := &Log{path: path, max: maxBytes}
	l.recent = append(l.recent, load(path+".1")...)
	l.recent = append(l.recent, load(path)...)
	if len(l.recent) > keep {
		l.recent = l.recent[len(l.recent)-keep:]
	}
	if err := l.openFile(); err != nil {
		return nil, err
	}
	return l, nil
}

func load(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
			if len(out) > keep {
				out = out[1:]
			}
		}
	}
	return out
}

func (l *Log) openFile() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

func (l *Log) Append(recs ...Record) error {
	l.mu.Lock()
	var errs []error
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		b = append(b, '\n')
		if l.size > 0 && l.size+int64(len(b)) > l.max {
			if err := l.rotate(); err != nil {
				errs = append(errs, err)
			}
		}
		n, err := l.f.Write(b)
		l.size += int64(n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		l.recent = append(l.recent, r)
		if len(l.recent) > keep {
			l.recent = l.recent[1:]
		}
	}
	subs := slices.Clone(l.subs)
	l.mu.Unlock()
	for _, r := range recs {
		for _, fn := range subs {
			fn(r)
		}
	}
	return errors.Join(errs...)
}

func (l *Log) rotate() error {
	l.f.Close()
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return errors.Join(err, l.openFile())
	}
	return l.openFile()
}

// Recent returns up to limit records, newest first. unit "" means all units.
func (l *Log) Recent(unit string, limit int) []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Record
	for i := len(l.recent) - 1; i >= 0 && len(out) < limit; i-- {
		if unit == "" || l.recent[i].Unit == unit {
			out = append(out, l.recent[i])
		}
	}
	return out
}

// ChangedSince returns the paths of unit/endpoint with a record after since.
func (l *Log) ChangedSince(unit, endpoint string, since time.Time) map[string]bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]bool{}
	for i := len(l.recent) - 1; i >= 0; i-- {
		r := l.recent[i]
		if r.Unit == unit && r.Endpoint == endpoint && r.T.After(since) {
			out[r.Path] = true
		}
	}
	return out
}

// Subscribe registers fn to be called after each appended record.
func (l *Log) Subscribe(fn func(Record)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.subs = append(l.subs, fn)
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
