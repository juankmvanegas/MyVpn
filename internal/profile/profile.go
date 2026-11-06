// Package profile stores the user's VPN connection profiles.
package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Profile is one VPN connection, equivalent to a FortiClient "SSL-VPN" entry
// with "Enable Single Sign On (SSO) for VPN Tunnel" checked.
type Profile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TrustedCert string `json:"trustedCert,omitempty"`
	DNSSplit    bool   `json:"dnsSplit"`
}

// Store is the on-disk list of profiles.
type Store struct {
	Profiles []Profile `json:"profiles"`
	Last     string    `json:"last"`
}

func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "MyVpn", "profiles.json"), nil
}

// Load reads the store (empty when no profile has been created yet).
func Load() (*Store, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s Store
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save writes the store.
func (s *Store) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// Get returns the profile with the given name.
func (s *Store) Get(name string) (Profile, bool) {
	for _, p := range s.Profiles {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Profile{}, false
}

// Upsert adds or replaces a profile; oldName allows renaming.
func (s *Store) Upsert(oldName string, p Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(p.Host, "https://"), "http://"))
	p.Host = strings.TrimSuffix(p.Host, "/")
	if p.Name == "" || p.Host == "" {
		return errors.New("el nombre y el gateway son obligatorios")
	}
	if p.Port <= 0 || p.Port > 65535 {
		p.Port = 443
	}
	for i, q := range s.Profiles {
		if strings.EqualFold(q.Name, oldName) || strings.EqualFold(q.Name, p.Name) {
			s.Profiles[i] = p
			return s.Save()
		}
	}
	s.Profiles = append(s.Profiles, p)
	return s.Save()
}

// Delete removes a profile.
func (s *Store) Delete(name string) error {
	out := s.Profiles[:0]
	for _, p := range s.Profiles {
		if !strings.EqualFold(p.Name, name) {
			out = append(out, p)
		}
	}
	s.Profiles = out
	return s.Save()
}
