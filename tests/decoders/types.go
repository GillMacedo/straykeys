// Package decoders holds struct types that exercise each decoder's key rules.
// The tests decode configs into them with the real libraries and compare the
// result with what straykeys reports.
package decoders

import (
	"time"

	"gopkg.in/yaml.v3"
)

// ---- gopkg.in/yaml.v3 ----

type YRoot struct {
	Name     string  `yaml:"name"`
	Server   YServer `yaml:"server"`
	YBase    `yaml:",inline"`
	Ptr      *YServer          `yaml:"ptr"`
	Items    []YItem           `yaml:"items"`
	Labels   map[string]string `yaml:"labels"`
	Nested   map[string]YItem  `yaml:"nested"`
	NoTag    int
	Skipped  string `yaml:"-"`
	hidden   int
	Timeout  time.Duration `yaml:"timeout"`
	Anything interface{}   `yaml:"anything"`
	Custom   YCustom       `yaml:"custom"`
	YEmbed
	Grid [][]YItem `yaml:"grid"`
}

type YBase struct {
	Region string `yaml:"region"`
	Zone   string `yaml:"zone"`
}

type YEmbed struct {
	Inner string `yaml:"inner"`
}

type YServer struct {
	Host    string        `yaml:"host"`
	Port    int           `yaml:"port"`
	TLS     *YTLS         `yaml:"tls"`
	Timeout time.Duration `yaml:"timeout"`
}

type YTLS struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type YItem struct {
	ID      string   `yaml:"id"`
	Weight  float64  `yaml:"weight"`
	Enabled bool     `yaml:"enabled"`
	Tags    []string `yaml:"tags"`
}

// YCustom keeps the whole node, so every key under it is used.
type YCustom struct{ Raw string }

func (c *YCustom) UnmarshalYAML(n *yaml.Node) error {
	out, err := yaml.Marshal(n)
	c.Raw = string(out)
	return err
}

// YCatch keeps unknown keys in an inline map.
type YCatch struct {
	Name  string         `yaml:"name"`
	Extra map[string]any `yaml:",inline"`
}

// ---- encoding/json and sigs.k8s.io/yaml ----

type JRoot struct {
	Name     string            `json:"name"`
	Server   JServer           `json:"server"`
	JBase                      // promoted
	JTagged  `json:"tagged"`   // not promoted: has a name
	Ptr      *JServer          `json:"ptr,omitempty"`
	Items    []JItem           `json:"items"`
	Labels   map[string]string `json:"labels"`
	NoTag    int
	Skipped  string `json:"-"`
	Dash     string `json:"-,"`
	hidden   int
	Timeout  time.Duration `json:"timeout"`
	Anything any           `json:"anything"`
	Count    int           `json:"count,string"`
	JConflictA
	JConflictB
	JDeep
}

type JBase struct {
	Region string `json:"region"`
	Name   string `json:"name"` // hidden by JRoot.Name, which is shallower
}

type JTagged struct {
	Inner string `json:"inner"`
}

// JConflictA and JConflictB both promote "shared" at the same depth without
// a tag winner, so encoding/json drops it.
type JConflictA struct {
	Shared string `json:"shared"`
	OnlyA  string `json:"only_a"`
}

type JConflictB struct {
	Shared string `json:"shared"`
}

type JDeep struct {
	JDeeper
}

type JDeeper struct {
	Level string `json:"level"`
}

type JServer struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  *JTLS  `json:"tls"`
}

type JTLS struct {
	Cert string `json:"cert"`
}

type JItem struct {
	ID      string   `json:"id"`
	Weight  float64  `json:"weight"`
	Enabled bool     `json:"enabled"`
	Tags    []string `json:"tags"`
}

// ---- viper (mapstructure tags) ----

type VRoot struct {
	Name     string  `mapstructure:"name"`
	Server   VServer `mapstructure:"server"`
	VBase    `mapstructure:",squash"`
	VEmbed                     // not squashed: key is "VEmbed"
	Ptr      *VServer          `mapstructure:"ptr"`
	Items    []VItem           `mapstructure:"items"`
	Labels   map[string]string `mapstructure:"labels"`
	NoTag    int
	Skipped  string `mapstructure:"-"`
	hidden   int
	Timeout  time.Duration `mapstructure:"timeout"`
	Anything any           `mapstructure:"anything"`
}

type VBase struct {
	Region string `mapstructure:"region"`
}

type VEmbed struct {
	Inner string `mapstructure:"inner"`
}

type VServer struct {
	Host    string        `mapstructure:"host"`
	Port    int           `mapstructure:"port"`
	TLS     *VTLS         `mapstructure:"tls"`
	Timeout time.Duration `mapstructure:"timeout"`
}

type VTLS struct {
	Cert string `mapstructure:"cert"`
}

type VItem struct {
	ID      string   `mapstructure:"id"`
	Weight  float64  `mapstructure:"weight"`
	Enabled bool     `mapstructure:"enabled"`
	Tags    []string `mapstructure:"tags"`
}

type VRemain struct {
	Name string         `mapstructure:"name"`
	Rest map[string]any `mapstructure:",remain"`
}

// ---- koanf (koanf tags) ----

type KRoot struct {
	Name    string  `koanf:"name"`
	Server  KServer `koanf:"server"`
	KBase   `koanf:",squash"`
	Items   []KItem           `koanf:"items"`
	Labels  map[string]string `koanf:"labels"`
	NoTag   int
	Timeout time.Duration `koanf:"timeout"`
}

type KBase struct {
	Region string `koanf:"region"`
}

type KServer struct {
	Host string `koanf:"host"`
	Port int    `koanf:"port"`
}

type KItem struct {
	ID     string  `koanf:"id"`
	Weight float64 `koanf:"weight"`
}
