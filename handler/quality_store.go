package handler

import (
	"sync"

	"github.com/google/uuid"
)

type QualityDefinition struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Resolution int    `json:"resolution"`
	Allowed    bool   `json:"allowed"`
}

type QualityProfile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	QualityIDs     []string `json:"quality_ids"`
	UpgradeAllowed bool     `json:"upgrade_allowed"`
	CutoffQuality  string   `json:"cutoff_quality"`
}

var defaultQualityDefs = []QualityDefinition{
	{ID: "sd-480p", Name: "SD 480p", Resolution: 480, Allowed: true},
	{ID: "hd-720p", Name: "HD 720p", Resolution: 720, Allowed: true},
	{ID: "fhd-1080p", Name: "FHD 1080p", Resolution: 1080, Allowed: true},
	{ID: "uhd-2160p", Name: "UHD 4K", Resolution: 2160, Allowed: true},
}

type QualityStore struct {
	mu       sync.RWMutex
	defs     map[string]QualityDefinition
	profiles map[string]QualityProfile
}

func NewQualityStore() *QualityStore {
	defs := make(map[string]QualityDefinition)
	for _, d := range defaultQualityDefs {
		defs[d.ID] = d
	}
	profiles := map[string]QualityProfile{
		"default": {
			ID:             "default",
			Name:           "Any HD",
			QualityIDs:     []string{"hd-720p", "fhd-1080p", "uhd-2160p"},
			UpgradeAllowed: true,
			CutoffQuality:  "fhd-1080p",
		},
	}
	return &QualityStore{
		defs:     defs,
		profiles: profiles,
	}
}

func (s *QualityStore) ListDefinitions() []QualityDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]QualityDefinition, 0, len(s.defs))
	for _, d := range s.defs {
		out = append(out, d)
	}
	return out
}

func (s *QualityStore) UpdateDefinition(d QualityDefinition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	s.defs[d.ID] = d
}

func (s *QualityStore) ListProfiles() []QualityProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]QualityProfile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p)
	}
	return out
}

func (s *QualityStore) GetProfile(id string) (QualityProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[id]
	return p, ok
}

func (s *QualityStore) SaveProfile(p QualityProfile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	s.profiles[p.ID] = p
}

func (s *QualityStore) DeleteProfile(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.profiles, id)
}
