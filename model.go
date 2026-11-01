package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Proxy struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Now     string   `json:"now"`
	All     []string `json:"all"`
	TestURL string   `json:"testUrl"`
}

func (p Proxy) switchable() bool {
	switch strings.ToLower(p.Type) {
	case "selector", "urltest", "fallback":
		return true
	}
	return false
}

func special(p Proxy) bool {
	switch strings.ToLower(p.Type) {
	case "direct", "reject", "rejectdrop", "pass", "compatible", "dns":
		return true
	}
	return false
}

type Node struct {
	Proxy
	Provider  string
	Ambiguous bool
}

type Snapshot struct {
	Proxies map[string]Proxy
	Nodes   map[string]Node
}

type Hop struct{ Group, Selected string }

type Route struct {
	Node Node
	URL  string
	Path []Hop
}

func (r Route) key() string {
	b, _ := json.Marshal([]string{r.Node.Provider, r.Node.Name, r.URL})
	return string(b)
}

func (s *Snapshot) resolve(name, probeURL string) (Route, error) {
	r := Route{URL: probeURL}
	seen := map[string]bool{}
	for {
		if seen[name] {
			return r, fmt.Errorf("cyclic selection")
		}
		seen[name] = true
		if p, ok := s.Proxies[name]; ok {
			if p.switchable() {
				if p.Now == "" || !contains(p.All, p.Now) {
					return r, fmt.Errorf("empty or invalid selection")
				}
				r.Path = append(r.Path, Hop{name, p.Now})
				if p.TestURL != "" {
					r.URL = p.TestURL
				}
				name = p.Now
				continue
			}
			if len(p.All) > 0 {
				return r, fmt.Errorf("unsupported group type %s", p.Type)
			}
		}
		n, ok := s.Nodes[name]
		if !ok {
			return r, fmt.Errorf("node not discovered")
		}
		if n.Ambiguous {
			return r, fmt.Errorf("ambiguous duplicate node name")
		}
		if special(n.Proxy) {
			return r, fmt.Errorf("direct or policy action; skip")
		}
		if len(n.All) > 0 {
			return r, fmt.Errorf("unsupported group")
		}
		r.Node = n
		return r, nil
	}
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func selectionID(s *Snapshot, name string, r Route) string {
	p := s.Proxies[name]
	b, _ := json.Marshal(struct {
		Type string
		All  []string
		Path []Hop
		Key  string
	}{p.Type, p.All, r.Path, r.key()})
	return string(b)
}
