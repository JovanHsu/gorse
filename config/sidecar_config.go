// Copyright 2024 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"flag"
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type SidecarServiceConfig struct {
	RedisAddr     string
	RedisPassword string
	DataStore     string
	HTTPHost      string
	HTTPPort      int
}

func LoadSidecarConfig() (*SidecarServiceConfig, error) {
	configPath := flag.String("config", "", "path to config file")
	httpPort := flag.Int("port", 0, "HTTP listen port (overrides config file)")
	flag.Parse()

	path := *configPath
	if path == "" {
		path = os.Getenv("GORSE_CONFIG")
	}
	if path == "" {
		return nil, fmt.Errorf("no config file specified: use --config or GORSE_CONFIG")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("toml")

	// Bind env vars as fallback
	v.BindEnv("sidecar.redis_addr", "REDIS_ADDR")
	v.BindEnv("sidecar.redis_password", "REDIS_PASSWORD")
	v.BindEnv("sidecar.data_store", "DATA_STORE_URI")
	v.BindEnv("sidecar.http_port", "HTTP_PORT")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	viperPort := v.GetInt("sidecar.http_port")
	if *httpPort > 0 {
		viperPort = *httpPort
	}

	cfg := &SidecarServiceConfig{
		RedisAddr:     v.GetString("sidecar.redis_addr"),
		RedisPassword: v.GetString("sidecar.redis_password"),
		DataStore:     v.GetString("sidecar.data_store"),
		HTTPHost:      v.GetString("sidecar.http_host"),
		HTTPPort:      viperPort,
	}

	if cfg.RedisAddr == "" || cfg.RedisPassword == "" {
		return nil, fmt.Errorf("sidecar.redis_addr and sidecar.redis_password are required")
	}

	return cfg, nil
}
