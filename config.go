package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Controller    string   `json:"controller"`
	SecretFile    string   `json:"secret_file"`
	Groups        []string `json:"groups"`
	ExcludeGroups []string `json:"exclude_groups"`
	IntervalMS    int      `json:"interval_ms"`
	TimeoutMS     int      `json:"timeout_ms"`
	ProbeURL      string   `json:"probe_url"`
	MaxConcurrent int      `json:"max_concurrent"`
	StatusFile    string   `json:"status_file"`
}

func defaultConfig() Config {
	return Config{
		Controller: "http://127.0.0.1:9090", IntervalMS: 1000, TimeoutMS: 800,
		ProbeURL: "https://www.gstatic.com/generate_204", MaxConcurrent: 16,
		StatusFile: "/tmp/node-guard/status.json",
	}
}

func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) validate() error {
	for _, raw := range []string{c.Controller, c.ProbeURL} {
		u, err := url.Parse(raw)
		if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("controller and probe_url must be HTTP(S) URLs without embedded credentials")
		}
	}
	u, _ := url.Parse(c.Controller)
	if u.RawQuery != "" {
		return fmt.Errorf("controller must not contain a query")
	}
	if c.IntervalMS < 100 || c.TimeoutMS < 100 || c.TimeoutMS > 30000 || c.MaxConcurrent < 1 || c.MaxConcurrent > 64 {
		return fmt.Errorf("invalid interval_ms, timeout_ms or max_concurrent")
	}
	return nil
}

func readSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	s := strings.TrimRight(string(b), "\r\n")
	if strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("secret must be one line")
	}
	return s, nil
}
