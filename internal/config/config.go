package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type CLIConfig struct {
	Version int       `toml:"version"`
	Repo    string    `toml:"repo"`
	Server  ServerRef `toml:"server"`
	Mount   Mount     `toml:"mount"`
	Docs    Docs      `toml:"docs"`
	Auth    Auth      `toml:"auth"`
	Cache   Cache     `toml:"cache"`
}

type ServerRef struct {
	Addr string `toml:"addr"`
}

type Mount struct {
	Include []string `toml:"include"`
	Exclude []string `toml:"exclude"`
}

type Docs struct {
	Path string `toml:"path"`
}

type Auth struct {
	Mode     string `toml:"mode"`
	TokenEnv string `toml:"token_env"`
}

type Cache struct {
	MetadataTTL string `toml:"metadata_ttl"`
	ContentTTL  string `toml:"content_ttl"`
	Materialize string `toml:"materialize"`
}

type MountsConfig struct {
	Version int           `toml:"version"`
	Mounts  []MountConfig `toml:"mounts"`
}

type MountConfig struct {
	Local  string `toml:"local"`
	Remote string `toml:"remote"`
	Mode   string `toml:"mode"`
	Source string `toml:"source"`
}

type ServerConfig struct {
	Addr  string       `toml:"addr"`
	Repos []RepoConfig `toml:"repos"`
}

type RepoConfig struct {
	Name     string        `toml:"name"`
	Backend  BackendConfig `toml:"backend"`
	Writable bool          `toml:"writable"` // allow cross-repo write-through to this repo
}

type BackendConfig struct {
	Type     string         `toml:"type"`
	Postgres PostgresConfig `toml:"postgres"`
}

type PostgresConfig struct {
	DSN            string              `toml:"dsn"`
	Schema         string              `toml:"schema"`
	NodesTable     string              `toml:"nodes_table"`
	ContentTable   string              `toml:"content_table"`
	RepoNodesTable string              `toml:"repo_nodes_table"`
	Files          PostgresFileMapping `toml:"files"`
	CacheTTL       string              `toml:"cache_ttl"`
}

type PostgresFileMapping struct {
	PathColumn  string `toml:"path_column"`
	KindColumn  string `toml:"kind_column"`
	SizeColumn  string `toml:"size_column"`
	MTimeColumn string `toml:"mtime_column"`
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
	if cfg.Repo == "" {
		return CLIConfig{}, fmt.Errorf("repo is required")
	}
	if cfg.Server.Addr == "" {
		return CLIConfig{}, fmt.Errorf("server.addr is required")
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Auth.Mode == "" {
		cfg.Auth.Mode = "none"
	}
	if cfg.Auth.Mode != "none" && cfg.Auth.Mode != "bearer" {
		return CLIConfig{}, fmt.Errorf("unsupported auth.mode %q", cfg.Auth.Mode)
	}
	if cfg.Auth.Mode == "bearer" && cfg.Auth.TokenEnv == "" {
		cfg.Auth.TokenEnv = "GXFS_TOKEN"
	}
	if cfg.Cache.MetadataTTL == "" {
		cfg.Cache.MetadataTTL = "5m"
	}
	if cfg.Cache.ContentTTL == "" {
		cfg.Cache.ContentTTL = "24h"
	}
	if cfg.Cache.Materialize == "" {
		cfg.Cache.Materialize = "explicit"
	}
	if cfg.Cache.Materialize != "explicit" && cfg.Cache.Materialize != "auto" {
		return CLIConfig{}, fmt.Errorf("unsupported cache.materialize %q: use explicit or auto", cfg.Cache.Materialize)
	}
	if len(cfg.Mount.Include) == 0 {
		cfg.Mount.Include = []string{"/"}
	}
	cfg.Docs.Path = cleanLocalPath(cfg.Docs.Path)
	if cfg.Docs.Path == "" {
		cfg.Docs.Path = "docs"
	}
	return cfg, nil
}

func LoadMounts(path string) (MountsConfig, error) {
	data, err := readExpanded(path)
	if err != nil {
		return MountsConfig{}, err
	}

	var cfg MountsConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return MountsConfig{}, fmt.Errorf("parse mounts config: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	for i := range cfg.Mounts {
		m := &cfg.Mounts[i]
		m.Local = cleanLocalPath(m.Local)
		if m.Local == "" {
			return MountsConfig{}, fmt.Errorf("mounts[%d].local is required", i)
		}
		if m.Remote == "" {
			return MountsConfig{}, fmt.Errorf("mounts[%d].remote is required", i)
		}
		if m.Mode == "" {
			m.Mode = "readonly"
		}
		if m.Mode != "readonly" && m.Mode != "writable" {
			return MountsConfig{}, fmt.Errorf("mounts[%d].mode must be readonly or writable", i)
		}
		if m.Source == "" {
			m.Source = "manual"
		}
	}
	return cfg, nil
}

func DefaultMounts(cfg CLIConfig) MountsConfig {
	docsPath := cleanLocalPath(cfg.Docs.Path)
	if docsPath == "" {
		docsPath = "docs"
	}
	return MountsConfig{
		Version: 1,
		Mounts: []MountConfig{{
			Local:  docsPath,
			Remote: "repo://self/" + docsPath,
			Mode:   "writable",
			Source: "default",
		}},
	}
}

func SaveMounts(path string, cfg MountsConfig) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal mounts config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create mounts dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write mounts config: %w", err)
	}
	return nil
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

func cleanLocalPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	return p
}
