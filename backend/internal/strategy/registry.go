package strategy

import (
	"fmt"
	"sort"
	"sync"
)

// registry is the Phase-2 static in-memory Registry. A DB-driven dynamic
// registry may replace it later; the interface stays.
type registry struct {
	mu      sync.RWMutex
	byID    map[string]Strategy
	ordered []string
}

// NewRegistry returns an empty Registry.
func NewRegistry() Registry {
	return &registry{byID: map[string]Strategy{}}
}

func (r *registry) Register(s Strategy) error {
	if s == nil || s.ID() == "" {
		return fmt.Errorf("strategy registry: nil or empty-ID strategy")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byID[s.ID()]; dup {
		return fmt.Errorf("strategy registry: duplicate ID %q", s.ID())
	}
	r.byID[s.ID()] = s
	r.ordered = append(r.ordered, s.ID())
	sort.Strings(r.ordered)
	return nil
}

func (r *registry) Get(id string) (Strategy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.byID[id]
	return s, ok
}

func (r *registry) List() []Strategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Strategy, 0, len(r.ordered))
	for _, id := range r.ordered {
		out = append(out, r.byID[id])
	}
	return out
}
