// Package config memuat konfigurasi runtime dari environment dengan nilai default
// yang aman untuk server pribadi.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config adalah konfigurasi runtime aplikasi.
type Config struct {
	Addr         string
	DataDir      string
	RepoOwner    string
	RepoName     string
	Branch       string
	GitHubTok    string
	GitHubAPI    string
	GiteaMode    bool
	AdminUser    string
	AdminPass    string
	TrustedProxy bool
	SessionTTL   time.Duration
	TLSCert      string
	TLSKey       string
}

// Load membaca konfigurasi dari environment.
func Load() Config {
	return Config{
		Addr:         env("TS_ADDR", "127.0.0.1:8080"),
		DataDir:      env("TS_DATA_DIR", "./data"),
		RepoOwner:    env("TS_REPO_OWNER", ""),
		RepoName:     env("TS_REPO_NAME", ""),
		Branch:       env("TS_REPO_BRANCH", "main"),
		GitHubTok:    env("TS_GITHUB_TOKEN", ""),
		GitHubAPI:    env("TS_GITHUB_API", "https://api.github.com"),
		GiteaMode:    envBool("TS_GITEA_MODE", false),
		AdminUser:    env("TS_ADMIN_USERNAME", "admin"),
		AdminPass:    env("TS_ADMIN_PASSWORD", ""),
		TrustedProxy: envBool("TS_TRUSTED_PROXY", false),
		SessionTTL:   time.Duration(envInt("TS_SESSION_TTL_HOURS", 24*7)) * time.Hour,
		TLSCert:      env("TS_TLS_CERT", ""),
		TLSKey:       env("TS_TLS_KEY", ""),
	}
}

// StagingDir direktori tempat file menunggu push ke GitHub.
func (c Config) StagingDir() string { return c.DataDir + "/staging" }

// DBPath lokasi berkas database SQLite.
func (c Config) DBPath() string { return c.DataDir + "/tugas.db" }

// GitHubReady memberi tahu apakah konfigurasi repo sudah lengkap.
func (c Config) GitHubReady() bool {
	return c.RepoOwner != "" && c.RepoName != "" && c.GitHubTok != ""
}

// GiteaReady memberi tahu apakah mode Gitea dikonfigurasi lengkap.
func (c Config) GiteaReady() bool {
	return c.GiteaMode && c.RepoOwner != "" && c.RepoName != "" && c.GitHubTok != ""
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}
