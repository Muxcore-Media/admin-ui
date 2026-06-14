package handler

import "sync"

type TagStore struct {
	mu   sync.RWMutex
	tags map[string]bool
}

func NewTagStore() *TagStore {
	return &TagStore{
		tags: map[string]bool{
			"favorite":    true,
			"watchlist":   true,
			"4k":          true,
			"hdr":         true,
			"foreign":     true,
			"documentary": true,
		},
	}
}

func (s *TagStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.tags))
	for t := range s.tags {
		out = append(out, t)
	}
	return out
}

func (s *TagStore) Add(name string) bool {
	if name == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[name] = true
	return true
}

func (s *TagStore) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tags, name)
}
