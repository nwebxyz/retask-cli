// internal/config/profile.go
package config

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultEndpoint = "api.dev.nweb.app:443"

type Profile struct {
	Endpoint     string    `yaml:"endpoint"`
	WorkspaceID  string    `yaml:"workspace_id,omitempty"`
	CachedJWT    string    `yaml:"cached_jwt,omitempty"`
	JWTExpiresAt time.Time `yaml:"jwt_expires_at,omitempty"`
}

type Config struct {
	ActiveProfile string             `yaml:"active_profile"`
	Profiles      map[string]Profile `yaml:"profiles"`
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "retask", "config.yaml")
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{
			ActiveProfile: "default",
			Profiles: map[string]Profile{
				"default": {Endpoint: DefaultEndpoint},
			},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ActiveProfileData returns the profile for the given name (or c.ActiveProfile if empty).
func (c *Config) ActiveProfileData(name string) Profile {
	if name == "" {
		name = c.ActiveProfile
	}
	if name == "" {
		name = "default"
	}
	p, ok := c.Profiles[name]
	if !ok {
		p = Profile{Endpoint: DefaultEndpoint}
	}
	if p.Endpoint == "" {
		p.Endpoint = DefaultEndpoint
	}
	return p
}

// SetProfile upserts a profile.
func (c *Config) SetProfile(name string, p Profile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	c.Profiles[name] = p
}
