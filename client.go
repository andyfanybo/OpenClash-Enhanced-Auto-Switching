package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Result struct {
	Health  string `json:"health"`
	DelayMS int    `json:"delay_ms,omitempty"`
}

const (
	healthy      = "healthy"
	unavailable  = "unavailable"
	controlError = "control_error"
)

type API interface {
	Snapshot(context.Context) (*Snapshot, error)
	Probe(context.Context, Route) Result
	Select(context.Context, string, string) error
	Group(context.Context, string) (Proxy, error)
}

type Client struct {
	base, secret string
	timeoutMS    int
	http         *http.Client
	providers    map[string]Node
	providerTime time.Time
}

type httpError struct{ Status int }

func (e httpError) Error() string { return fmt.Sprintf("controller HTTP %d", e.Status) }

func newClient(c Config, secret string) *Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // Explicit controller connection; never send its secret through an environment proxy.
	t.MaxIdleConnsPerHost = c.MaxConcurrent + 2
	return &Client{base: strings.TrimRight(c.Controller, "/"), secret: secret, timeoutMS: c.TimeoutMS,
		http: &http.Client{Transport: t, Timeout: time.Duration(c.TimeoutMS+700) * time.Millisecond,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid controller request")
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("controller transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError{resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
			return fmt.Errorf("invalid controller JSON")
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	}
	return nil
}

func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var payload struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	if err := c.request(ctx, "GET", "/proxies", nil, &payload); err != nil {
		return nil, err
	}
	if payload.Proxies == nil {
		return nil, fmt.Errorf("missing proxies in controller response")
	}
	refresh := c.providers == nil || time.Since(c.providerTime) > 30*time.Second
	for _, p := range payload.Proxies {
		for _, member := range p.All {
			if _, ok := payload.Proxies[member]; !ok {
				if _, ok := c.providers[member]; !ok {
					refresh = true
				}
			}
		}
	}
	if refresh {
		var providers struct {
			Providers map[string]struct {
				Vehicle string  `json:"vehicleType"`
				Proxies []Proxy `json:"proxies"`
			} `json:"providers"`
		}
		if err := c.request(ctx, "GET", "/providers/proxies", nil, &providers); err != nil {
			return nil, err
		}
		nodes := map[string]Node{}
		for provider, p := range providers.Providers {
			if strings.EqualFold(p.Vehicle, "Compatible") {
				continue
			}
			for _, n := range p.Proxies {
				node := Node{Proxy: n, Provider: provider}
				if prev, ok := nodes[n.Name]; ok && prev.Provider != provider {
					node.Ambiguous = true
				}
				nodes[n.Name] = node
			}
		}
		c.providers, c.providerTime = nodes, time.Now()
	}
	s := &Snapshot{Proxies: payload.Proxies, Nodes: map[string]Node{}}
	for name, n := range c.providers {
		s.Nodes[name] = n
	}
	for name, p := range payload.Proxies {
		p.Name = name
		s.Proxies[name] = p
		if !p.switchable() && len(p.All) == 0 {
			n := Node{Proxy: p}
			if _, exists := s.Nodes[name]; exists {
				n.Ambiguous = true
			}
			s.Nodes[name] = n
		}
	}
	return s, nil
}

func (c *Client) Probe(ctx context.Context, r Route) Result {
	path := "/proxies/" + url.PathEscape(r.Node.Name) + "/delay"
	if r.Node.Provider != "" {
		path = "/providers/proxies/" + url.PathEscape(r.Node.Provider) + "/" + url.PathEscape(r.Node.Name) + "/healthcheck"
	}
	q := url.Values{"url": {r.URL}, "timeout": {strconv.Itoa(c.timeoutMS)}}
	var out struct {
		Delay *int `json:"delay"`
	}
	err := c.request(ctx, "GET", path+"?"+q.Encode(), nil, &out)
	if err != nil {
		if e, ok := err.(httpError); ok && (e.Status == 503 || e.Status == 504) {
			return Result{Health: unavailable}
		}
		return Result{Health: controlError}
	}
	if out.Delay == nil || *out.Delay <= 0 {
		return Result{Health: controlError}
	}
	return Result{Health: healthy, DelayMS: *out.Delay}
}

func (c *Client) Select(ctx context.Context, group, name string) error {
	return c.request(ctx, "PUT", "/proxies/"+url.PathEscape(group), map[string]string{"name": name}, nil)
}

func (c *Client) Group(ctx context.Context, name string) (Proxy, error) {
	var p Proxy
	err := c.request(ctx, "GET", "/proxies/"+url.PathEscape(name), nil, &p)
	return p, err
}
