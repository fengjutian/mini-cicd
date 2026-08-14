package logstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Sequence int64  `json:"sequence"`
	Time     string `json:"time"`
	StepID   int64  `json:"stepId,omitempty"`
	Stream   string `json:"stream"`
	Message  string `json:"message"`
}
type Store struct {
	root        string
	max         int64
	mu          sync.Mutex
	writers     map[int64]*Writer
	subscribers map[int64]map[chan Entry]struct{}
}
type Writer struct {
	store        *Store
	deploymentID int64
	file         *os.File
	size         int64
	sequence     int64
	secrets      []string
	truncated    bool
	mu           sync.Mutex
}

func New(dataDir string, max int64) (*Store, error) {
	root := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Store{root: root, max: max, writers: map[int64]*Writer{}, subscribers: map[int64]map[chan Entry]struct{}{}}, nil
}
func (s *Store) Path(projectID string, id int64) string {
	return filepath.Join(s.root, projectID, fmt.Sprintf("%d.jsonl", id))
}
func (s *Store) Open(projectID string, id int64, secrets []string) (*Writer, error) {
	path := s.Path(projectID, id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	w := &Writer{store: s, deploymentID: id, file: f, size: info.Size(), secrets: filterSecrets(secrets)}
	s.mu.Lock()
	s.writers[id] = w
	s.mu.Unlock()
	return w, nil
}
func (w *Writer) WriteStep(stepID int64, stream, message string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	message = redact(message, w.secrets)
	entry := Entry{Sequence: w.sequence + 1, Time: time.Now().UTC().Format(time.RFC3339Nano), StepID: stepID, Stream: stream, Message: message}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if w.size+int64(len(raw)) > w.store.max {
		if w.truncated {
			return nil
		}
		w.truncated = true
		entry.Message = "[mini-ci-cd] log truncated: size limit reached"
		entry.Stream = "system"
		raw, _ = json.Marshal(entry)
		raw = append(raw, '\n')
	}
	n, err := w.file.Write(raw)
	if err != nil {
		return err
	}
	w.size += int64(n)
	w.sequence = entry.Sequence
	w.store.publish(w.deploymentID, entry)
	return nil
}
func (w *Writer) Write(p []byte) (int, error) {
	if err := w.WriteStep(0, "output", string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (w *Writer) Close() error {
	w.store.mu.Lock()
	delete(w.store.writers, w.deploymentID)
	w.store.mu.Unlock()
	return w.file.Close()
}
func (s *Store) publish(id int64, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers[id] {
		select {
		case ch <- e:
		default:
			delete(s.subscribers[id], ch)
			close(ch)
		}
	}
}
func (s *Store) Subscribe(id int64) (chan Entry, func()) {
	ch := make(chan Entry, 64)
	s.mu.Lock()
	if s.subscribers[id] == nil {
		s.subscribers[id] = map[chan Entry]struct{}{}
	}
	s.subscribers[id][ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() { s.mu.Lock(); delete(s.subscribers[id], ch); close(ch); s.mu.Unlock() }
}
func (s *Store) ReadAfter(projectID string, id, after int64) ([]Entry, error) {
	f, err := os.Open(s.Path(projectID, id))
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []Entry{}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		var e Entry
		if json.Unmarshal(scan.Bytes(), &e) == nil && e.Sequence > after {
			out = append(out, e)
		}
	}
	return out, scan.Err()
}
func filterSecrets(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, "********")
	}
	return value
}
