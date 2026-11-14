// Package config is read with viper.Unmarshal. It imports nothing, so the
// test needs no downloaded modules.
package config

import "time"

type Config struct {
	Server  Server        `mapstructure:"server"`
	Timeout time.Duration `mapstructure:"timeout"`
}

type Server struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}
