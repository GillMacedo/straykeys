// Package billing is a made-up service used to show straykeys. Its config is
// read with gopkg.in/yaml.v3, which ignores keys that match no field.
package billing

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen   string          `yaml:"listen"`
	Database Database        `yaml:"database"`
	Retry    Retry           `yaml:"retry"`
	Invoices Invoices        `yaml:"invoices"`
	Shutdown time.Duration   `yaml:"shutdown_timeout"`
	Features map[string]bool `yaml:"features"`
}

type Database struct {
	URL          string        `yaml:"url"`
	MaxOpenConns int           `yaml:"max_open_conns"`
	ConnTimeout  time.Duration `yaml:"conn_timeout"`
}

type Retry struct {
	Attempts int           `yaml:"attempts"`
	Backoff  time.Duration `yaml:"backoff"`
}

type Invoices struct {
	Currency string   `yaml:"currency"`
	DueDays  int      `yaml:"due_days"`
	Notify   []Notify `yaml:"notify"`
}

type Notify struct {
	Email    string `yaml:"email"`
	OnFailed bool   `yaml:"on_failed"`
}

// Load reads the config file the way the service does.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
