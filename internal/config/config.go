package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type CLIConfig struct {
	Project string    `toml:"project"`
	Server  ServerRef `toml:"server"`
	Mount   Mount     `toml:"mount"`
}

type ServerRef struct {
	Addr string `toml:"addr"`
}

type Mount struct {
	Include []string `toml:"include"`
	Exclude []string `toml:"exclude"`
}

type ServerConfig struct {
	Addr  string       `toml:"addr"`
	Repos []RepoConfig `toml:"repos"`
}

type RepoConfig struct {
	Name    string        `toml:"name"`
	Backend BackendConfig `toml:"backend"`
}

type BackendConfig struct {
	Type     string         `toml:"type"`
	Postgres PostgresConfig `toml:"postgres"`
}

type PostgresConfig struct {
	DSN    string              `toml:"dsn"`
	Schema string              `toml:"schema"`
	Files  PostgresFileMapping `toml:"files"`
}

type PostgresFileMapping struct {
	Table         string `toml:"table"`
	PathColumn    string `toml:"path_column"`
	ContentColumn string `toml:"content_column"`
	SizeColumn    string `toml:"size_column"`
	MTimeColumn   string `toml:"mtime_column"`
}

func LoadCLI(path string) (CLIConfig, error) {
	data, err := readExpanded(path)
	if err != nil {
		return CLIConfig{}, err
	}

	var top map[string]any
	if err := toml.Unmarshal(data, &top); err != nil {
		return CLIConfig{}, fmt.Errorf("parse cli config: %w", err)
	}
	if _, ok := top["backend"]; ok {
		return CLIConfig{}, fmt.Errorf("cli config must not contain backend settings")
	}

	var cfg CLIConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return CLIConfig{}, fmt.Errorf("parse cli config: %w", err)
	}
	if cfg.Project == "" {
		return CLIConfig{}, fmt.Errorf("project is required")
	}
	if cfg.Server.Addr == "" {
		return CLIConfig{}, fmt.Errorf("server.addr is required")
	}
	if len(cfg.Mount.Include) == 0 {
		cfg.Mount.Include = []string{"/"}
	}
	return cfg, nil
}

func LoadServer(path string) (ServerConfig, error) {
	data, err := readExpanded(path)
	if err != nil {
		return ServerConfig{}, err
	}

	var cfg ServerConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return ServerConfig{}, fmt.Errorf("parse server config: %w", err)
	}
	if cfg.Addr == "" {
		return ServerConfig{}, fmt.Errorf("addr is required")
	}
	for i, repo := range cfg.Repos {
		if repo.Name == "" {
			return ServerConfig{}, fmt.Errorf("repos[%d].name is required", i)
		}
		if repo.Backend.Type == "" {
			return ServerConfig{}, fmt.Errorf("repos[%d].backend.type is required", i)
		}
	}
	return cfg, nil
}

func readExpanded(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return []byte(os.ExpandEnv(string(data))), nil
}
