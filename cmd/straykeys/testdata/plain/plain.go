package plain

import "encoding/json"

var DefaultConfig = Config{Port: 9090}

// Config sets defaults, then decodes a plain copy: its keys are its fields.
type Config struct {
	Host   string  `yaml:"host" json:"host"`
	Port   int     `yaml:"port" json:"port"`
	Module *Module `yaml:"module" json:"module"`
}

func (c *Config) UnmarshalYAML(unmarshal func(any) error) error {
	*c = DefaultConfig
	type plain Config
	return unmarshal((*plain)(c))
}

func (c *Config) UnmarshalJSON(b []byte) error {
	*c = DefaultConfig
	type plain Config
	return json.Unmarshal(b, (*plain)(c))
}

// Module reads its own keys, so they can't be checked.
type Module struct{ raw map[string]any }

func (m *Module) UnmarshalYAML(unmarshal func(any) error) error {
	return unmarshal(&m.raw)
}

func (m *Module) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &m.raw)
}
