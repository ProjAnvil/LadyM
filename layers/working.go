// Package layers holds LadyM's five memory layers (L0–L4).
package layers

import (
	"sync"

	"github.com/ProjAnvil/LadyM/schema"
)

// WorkingMemory is a thread-safe bounded buffer of L0 notes.
type WorkingMemory struct {
	capacity  int
	workspace string
	mu        sync.Mutex
	items     []*schema.Memory
}

// NewWorkingMemory returns a WorkingMemory of the given capacity.
func NewWorkingMemory(capacity int, workspace string) *WorkingMemory {
	if capacity <= 0 {
		capacity = 64
	}
	if workspace == "" {
		workspace = "default"
	}
	return &WorkingMemory{capacity: capacity, workspace: workspace}
}

// Push appends an L0 note (dropping the oldest when at capacity).
func (w *WorkingMemory) Push(content string, tags []string, metadata map[string]any, source string) *schema.Memory {
	if tags == nil {
		tags = []string{}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	m := schema.NewMemory(schema.LayerWorking, schema.TypeNote)
	m.Content = content
	m.Summary = truncate(content, 80)
	m.Tags = tags
	m.Metadata = metadata
	m.Source = source
	m.Workspace = w.workspace
	w.mu.Lock()
	defer w.mu.Unlock()
	w.items = append(w.items, m)
	if len(w.items) > w.capacity {
		w.items = w.items[len(w.items)-w.capacity:]
	}
	return m
}

// Len returns the number of buffered items.
func (w *WorkingMemory) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.items)
}

// Items returns a snapshot of the buffered items.
func (w *WorkingMemory) Items() []*schema.Memory {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*schema.Memory, len(w.items))
	copy(out, w.items)
	return out
}

// Drain pops and returns all items (used by consolidate to flush L0 → L1).
func (w *WorkingMemory) Drain() []*schema.Memory {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.items
	w.items = nil
	return out
}

// Clear empties the buffer.
func (w *WorkingMemory) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.items = nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
