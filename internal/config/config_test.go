package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrap(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "127.0.0.1:8500")
	data, e := os.ReadFile("../../config/example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	sample := filepath.Join(t.TempDir(), "sample.yaml")
	os.WriteFile(sample, data, 0600)
	c, e := Load(sample)
	if e != nil {
		t.Fatal(e)
	}
	for name, mutate := range map[string]func(*Config){"consul": func(c *Config) { c.Consul.Address = "" }, "prefix": func(c *Config) { c.Consul.MetadataPrefix = "/" }, "body": func(c *Config) { c.Server.MaxRequestBodyBytes = 0 }, "timeout": func(c *Config) { c.Server.ReadTimeout = 0 }} {
		t.Run(name, func(t *testing.T) {
			v := *c
			mutate(&v)
			if v.Validate() == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}

	p := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(p, append(data, []byte("services: []\n")...), 0600)
	if _, e := Load(p); e == nil {
		t.Fatal("accepted business services")
	}
}

func TestTokenFileRelativeToConfig(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "127.0.0.1:8500")
	data, err := os.ReadFile("../../config/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	data = []byte(strings.ReplaceAll(string(data), `tokenEnv: "CONSUL_HTTP_TOKEN"`, `tokenFile: "consul-token"`))
	p := filepath.Join(dir, "gateway.yaml")
	os.WriteFile(p, data, 0600)
	os.WriteFile(filepath.Join(dir, "consul-token"), []byte("dedicated-test-token\n"), 0600)
	c, err := Load(p)
	if err != nil || c.Consul.Token != "dedicated-test-token" {
		t.Fatal("token file not loaded", err)
	}
	os.Remove(filepath.Join(dir, "consul-token"))
	if _, err := Load(p); err == nil {
		t.Fatal("missing token silently ignored")
	}
}

func TestConsulAddressEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, input, address, scheme string
		valid                        bool
	}{
		{"missing", "", "", "", false},
		{"host_port", "consul.example:8500", "consul.example:8500", "http", true},
		{"https", "https://consul.example:8501", "consul.example:8501", "https", true},
		{"http", "http://consul.example:8500/", "consul.example:8500", "http", true},
		{"credentials", "https://user:test-password@consul.example:8501", "", "", false},
		{"path", "https://consul.example/v1", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CONSUL_HTTP_ADDR", tc.input)
			c, err := Load("../../config/example.yaml")
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "test-password") {
					t.Fatal("credential leaked")
				}
				return
			}
			if c.Consul.Address != tc.address || c.Consul.Scheme != tc.scheme || c.Consul.TokenEnv != "CONSUL_HTTP_TOKEN" {
				t.Fatal("incorrect environment configuration")
			}
			if c.Server.Address != "0.0.0.0:8080" || c.Mcp.Address != "0.0.0.0:8081" || c.Admin.Address != "127.0.0.1:9090" {
				t.Fatal("unexpected default listener ports")
			}
		})
	}
}
