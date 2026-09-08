package config

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Address             string        `yaml:"address"`
	ReadTimeout         time.Duration `yaml:"readTimeout"`
	WriteTimeout        time.Duration `yaml:"writeTimeout"`
	IdleTimeout         time.Duration `yaml:"idleTimeout"`
	MaxRequestBodyBytes int           `yaml:"maxRequestBodyBytes"`
}
type ConsulConfig struct {
	Address        string `yaml:"address"`
	Scheme         string `yaml:"scheme"`
	TokenEnv       string `yaml:"tokenEnv"`
	TokenFile      string `yaml:"tokenFile"`
	Token          string `yaml:"-"`
	MetadataPrefix string `yaml:"metadataPrefix"`
}
type AdminConfig struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
}
type McpConfig struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
}
type Config struct {
	Mcp    McpConfig    `yaml:"mcp"`
	Server ServerConfig `yaml:"server"`
	Consul ConsulConfig `yaml:"consul"`
	Admin  AdminConfig  `yaml:"admin"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := &Config{}
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err = d.Decode(c); err != nil {
		return nil, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one configuration document")
	}
	if address, ok := os.LookupEnv("CONSUL_HTTP_ADDR"); ok {
		c.Consul.Address = strings.TrimSpace(address)
	}
	if strings.Contains(c.Consul.Address, "://") {
		u, err := url.Parse(c.Consul.Address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return nil, fmt.Errorf("CONSUL_HTTP_ADDR must be host:port or an HTTP(S) URL without credentials, path or query")
		}
		c.Consul.Address, c.Consul.Scheme = u.Host, u.Scheme
	}
	if c.Consul.Scheme == "" {
		c.Consul.Scheme = "http"
	}
	if c.Consul.TokenEnv == "" {
		c.Consul.TokenEnv = "CONSUL_HTTP_TOKEN"
	}
	if c.Consul.TokenFile != "" {
		tokenPath := c.Consul.TokenFile
		if !filepath.IsAbs(tokenPath) {
			tokenPath = filepath.Join(filepath.Dir(path), tokenPath)
		}
		data, err := os.ReadFile(tokenPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read Consul token file")
		}
		c.Consul.Token = strings.TrimSpace(string(data))
		if c.Consul.Token == "" {
			return nil, fmt.Errorf("Consul token file is empty")
		}
	}
	return c, c.Validate()
}
func (c *Config) Validate() error {
	if c.Mcp.Address == "" {
		c.Mcp.Address = "0.0.0.0:8081"
	}
	if c.Mcp.Enabled {
		if _, _, err := net.SplitHostPort(c.Mcp.Address); err != nil {
			return fmt.Errorf("invalid MCP address")
		}
		if c.Mcp.Address == c.Server.Address || c.Mcp.Address == c.Admin.Address {
			return fmt.Errorf("MCP address must differ from HTTP/Admin")
		}
	}
	if _, _, err := net.SplitHostPort(c.Server.Address); err != nil {
		return fmt.Errorf("invalid server address")
	}
	if c.Server.ReadTimeout <= 0 || c.Server.WriteTimeout <= 0 || c.Server.IdleTimeout <= 0 || c.Server.MaxRequestBodyBytes <= 0 {
		return fmt.Errorf("server timeouts and body limit must be positive")
	}
	if c.Consul.Address == "" || strings.Trim(c.Consul.MetadataPrefix, "/ ") == "" {
		return fmt.Errorf("Consul address is required (set CONSUL_HTTP_ADDR); metadataPrefix must not be empty")
	}
	if c.Consul.Scheme != "http" && c.Consul.Scheme != "https" {
		return fmt.Errorf("invalid consul scheme")
	}
	if c.Admin.Enabled {
		if _, _, err := net.SplitHostPort(c.Admin.Address); err != nil {
			return fmt.Errorf("invalid admin address")
		}
		if c.Admin.Address == c.Server.Address {
			return fmt.Errorf("admin and data addresses must differ")
		}
	}
	return nil
}
