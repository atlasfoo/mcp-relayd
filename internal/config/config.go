// Package config loads and validates the daemon configuration before startup.
package config

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config contains validated runtime settings.
type Config struct {
	Gateway Gateway
	Proxy   Proxy
	Servers map[string]Server
}

// Gateway describes the public loopback listener and its shutdown budget.
type Gateway struct {
	Host            string
	Port            int
	ShutdownTimeout time.Duration
}

// Proxy describes the bridge executable and its startup budget.
type Proxy struct {
	Command        string
	StartupTimeout time.Duration
}

// Server describes a single upstream stdio server.
type Server struct {
	Command   string
	Args      []string
	CWD       string
	Env       map[string]string
	Autostart bool
}

type fileConfig struct {
	Gateway struct {
		Host            string `toml:"host"`
		Port            int    `toml:"port"`
		ShutdownTimeout string `toml:"shutdown_timeout"`
	} `toml:"gateway"`
	Proxy struct {
		Command        string `toml:"command"`
		StartupTimeout string `toml:"startup_timeout"`
	} `toml:"proxy"`
	Servers map[string]fileServer `toml:"servers"`
}

type fileServer struct {
	Command   string            `toml:"command"`
	Args      []string          `toml:"args"`
	CWD       string            `toml:"cwd"`
	Env       map[string]string `toml:"env"`
	Autostart *bool             `toml:"autostart"`
}

var (
	routeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	variable  = regexp.MustCompile(`\$\{([^{}]*)\}`)
)

// DefaultPath returns the global per-user configuration path.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for configuration: %w", err)
	}
	return DefaultPathForHome(home)
}

// DefaultPathForHome builds the default path without consulting the working directory.
func DefaultPathForHome(home string) (string, error) {
	if home == "" || !filepath.IsAbs(home) {
		return "", fmt.Errorf("configuration requires an absolute user home directory")
	}
	return filepath.Join(home, ".config", "mcp-relayd.toml"), nil
}

// Load reads strict TOML and validates all settings before returning them.
// An empty path selects DefaultPath; explicit relative paths use the caller's cwd.
func Load(path string) (Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Config{}, err
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration path: %w", err)
	}
	content, err := os.ReadFile(path) // #nosec G304 -- The user intentionally selects a local configuration file.
	if err != nil {
		return Config{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	var raw fileConfig
	raw.Gateway.Host = "127.0.0.1"
	raw.Gateway.Port = 9876
	raw.Gateway.ShutdownTimeout = "10s"
	raw.Proxy.Command = "mcp-proxy"
	raw.Proxy.StartupTimeout = "30s"
	if err := toml.NewDecoder(strings.NewReader(string(content))).DisallowUnknownFields().Decode(&raw); err != nil {
		// Decoder diagnostics may contain source lines, including environment values.
		return Config{}, fmt.Errorf("configuration %q contains invalid TOML or unknown fields", path)
	}
	return validate(raw, filepath.Dir(path))
}

func validate(raw fileConfig, directory string) (Config, error) {
	if err := validateHost(raw.Gateway.Host); err != nil {
		return Config{}, err
	}
	if raw.Gateway.Port < 1 || raw.Gateway.Port > 65535 {
		return Config{}, fmt.Errorf("gateway.port must be between 1 and 65535")
	}
	if strings.TrimSpace(raw.Proxy.Command) == "" {
		return Config{}, fmt.Errorf("proxy.command must not be empty")
	}
	shutdownTimeout, err := positiveDuration(raw.Gateway.ShutdownTimeout, "gateway.shutdown_timeout")
	if err != nil {
		return Config{}, err
	}
	startupTimeout, err := positiveDuration(raw.Proxy.StartupTimeout, "proxy.startup_timeout")
	if err != nil {
		return Config{}, err
	}
	result := Config{
		Gateway: Gateway{Host: raw.Gateway.Host, Port: raw.Gateway.Port, ShutdownTimeout: shutdownTimeout},
		Proxy:   Proxy{Command: raw.Proxy.Command, StartupTimeout: startupTimeout},
		Servers: make(map[string]Server, len(raw.Servers)),
	}
	for name, rawServer := range raw.Servers {
		if !routeName.MatchString(name) {
			return Config{}, fmt.Errorf("server name must contain only route-safe letters, digits, underscores and hyphens")
		}
		server, err := validateServer(rawServer, directory)
		if err != nil {
			return Config{}, fmt.Errorf("server %q: %w", name, err)
		}
		result.Servers[name] = server
	}
	return result, nil
}

func validateHost(host string) error {
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err == nil && len(addresses) > 0 {
			for _, address := range addresses {
				if !address.IP.IsLoopback() {
					return fmt.Errorf("gateway.host must resolve exclusively to loopback addresses")
				}
			}
			return nil
		}
	}
	return fmt.Errorf("gateway.host must be a loopback IP or localhost resolving exclusively to loopback")
}

func positiveDuration(value, field string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", field)
	}
	return duration, nil
}

func validateServer(raw fileServer, directory string) (Server, error) {
	if strings.TrimSpace(raw.Command) == "" {
		return Server{}, fmt.Errorf("command must not be empty")
	}
	server := Server{Command: raw.Command, Args: raw.Args, CWD: raw.CWD, Env: make(map[string]string, len(raw.Env)), Autostart: true}
	if raw.Autostart != nil {
		server.Autostart = *raw.Autostart
	}
	if server.CWD != "" {
		if !filepath.IsAbs(server.CWD) {
			server.CWD = filepath.Join(directory, server.CWD)
		}
		info, err := os.Stat(server.CWD)
		if err != nil || !info.IsDir() {
			return Server{}, fmt.Errorf("cwd must be an existing directory")
		}
	}
	for key, value := range raw.Env {
		missing := false
		server.Env[key] = variable.ReplaceAllStringFunc(value, func(match string) string {
			value, exists := os.LookupEnv(match[2 : len(match)-1])
			if !exists || value == "" {
				missing = true
			}
			return value
		})
		if missing {
			return Server{}, fmt.Errorf("env contains an unavailable environment variable")
		}
	}
	return server, nil
}
