package main

// This file is parsed, not built: the test module doesn't require viper.

import (
	v "github.com/spf13/viper"
)

func main() {
	_ = v.GetString("log.level")
	_ = v.GetStringMap("plugins")
	_ = v.GetInt("server.workers")

	// An instance returned by viper.New() reads keys too.
	inst := v.New()
	_ = inst.GetString("log.format")
}
