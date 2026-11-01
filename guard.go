package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type GroupState struct {
	Group    string `json:"group"`
	Type     string `json:"type"`
	Selected string `json:"selected"`
	Node     string `json:"node,omitempty"`
	Result
	Note string `json:"note,omitempty"`
}

type Status struct {
	UpdatedAt time.Time    `json:"updated_at"`
	Observe   bool         `json:"observe"`
	Rounds    uint64       `json:"rounds"`
	Probes    uint64       `json:"probes"`
	Switches  uint64       `json:"switches"`
	CycleMS   int64        `json:"cycle_ms"`
	LastError string       `json:"last_error,omitempty"`
	Groups    []GroupState `json:"groups"`
	Events    []string     `json:"events"`
}

type cursor struct {
	ID   string
	Last string
}
type pendingCandidate struct {
	ID    string
	Name  string
	Route Route
}
type Guard struct {
	cfg     Config
	api     API
	observe bool
	cursors map[string]cursor
	pending map[string]pendingCandidate
	status  Status
}

func newGuard(c Config, api API, observe bool) *Guard {
	return &Guard{cfg: c, api: api, observe: observe, cursors: map[string]cursor{}, pending: map[string]pendingCandidate{}, status: Status{Observe: observe}}
}

func (g *Guard) event(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	log.Print(s)
	g.status.Events = append(g.status.Events, time.Now().Format(time.RFC3339)+" "+s)
	if len(g.status.Events) > 50 {
		g.status.Events = g.status.Events[len(g.status.Events)-50:]
	}
}

func (g *Guard) save() {
	g.status.UpdatedAt = time.Now()
	if g.cfg.StatusFile == "" {
		return
	}
	data, err := json.MarshalIndent(g.status, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(g.cfg.StatusFile)
	if err = os.MkdirAll(dir, 0700); err != nil {
		log.Print("status directory unavailable")
		return
	}
	f, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		log.Print("status file unavailable")
		return
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return
	}
	if err = f.Close(); err == nil {
		_ = os.Rename(name, g.cfg.StatusFile)
	}
}

func (g *Guard) cycle(ctx context.Context) {
	started := time.Now()
	g.status.Rounds++
	defer func() { g.status.CycleMS = time.Since(started).Milliseconds(); g.save() }()
	s, err := g.api.Snapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if g.status.LastError != err.Error() {
			g.event("control error: %s", err)
		}
		g.status.LastError = err.Error()
		g.status.Groups = nil
		return
	}
	g.status.LastError = ""
	names := []string{}
	for name, p := range s.Proxies {
		if !p.switchable() || name == "GLOBAL" || contains(g.cfg.ExcludeGroups, name) {
			continue
		}
		if len(g.cfg.Groups) > 0 && !contains(g.cfg.Groups, name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	groups := map[string]*GroupState{}
	routes := map[string]Route{}
	jobs := map[string]Route{}
	for _, name := range names {
		p := s.Proxies[name]
		st := &GroupState{Group: name, Type: p.Type, Selected: p.Now}
		groups[name] = st
		r, err := s.resolve(name, g.cfg.ProbeURL)
		if err != nil {
			st.Health = "skipped"
			st.Note = err.Error()
			continue
		}
		st.Node = r.Node.Name
		routes[name] = r
		jobs[r.key()] = r
	}
	// Candidates are checked in the next regular batch, never serially between ticks.
	for name, p := range g.pending {
		r, ok := routes[name]
		if !ok || selectionID(s, name, r) != p.ID {
			delete(g.pending, name)
			continue
		}
		candidate, err := s.resolve(p.Name, r.URL)
		if err != nil || candidate.key() != p.Route.key() || !contains(s.Proxies[name].All, p.Name) {
			delete(g.pending, name)
			continue
		}
		jobs[candidate.key()] = candidate
	}
	results := map[string]Result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, g.cfg.MaxConcurrent)
	for key, r := range jobs {
		wg.Add(1)
		go func(key string, r Route) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			result := g.api.Probe(ctx, r)
			mu.Lock()
			results[key] = result
			mu.Unlock()
		}(key, r)
	}
	wg.Wait()
	g.status.Probes += uint64(len(results))
	for name, r := range routes {
		groups[name].Result = results[r.key()]
	}
	// Country/inner groups first. Parents are re-resolved after each committed switch.
	order := append([]string(nil), names...)
	sort.SliceStable(order, func(i, j int) bool { return len(routes[order[i]].Path) < len(routes[order[j]].Path) })
	for _, name := range order {
		original, ok := routes[name]
		if !ok {
			continue
		}
		st := groups[name]
		if st.Health != unavailable {
			delete(g.cursors, name)
			delete(g.pending, name)
			continue
		}
		if ctx.Err() != nil {
			break
		}
		current, err := s.resolve(name, g.cfg.ProbeURL)
		delete(g.pending, name)
		if err != nil || current.key() != original.key() || selectionID(s, name, current) != selectionID(s, name, original) {
			st.Note = "selection changed through child group"
			continue
		}
		p := s.Proxies[name]
		id := selectionID(s, name, current)
		cur := g.cursors[name]
		start := p.Now
		if cur.ID == id {
			start = cur.Last
		}
		index := -1
		for i, member := range p.All {
			if member == start {
				index = i
				break
			}
		}
		if index < 0 {
			continue
		}
		st.Note = "no available next candidate"
		for offset := 1; offset <= len(p.All); offset++ {
			candidate := p.All[(index+offset)%len(p.All)]
			if candidate == p.Now {
				continue
			}
			candidateRoute, e := s.resolve(candidate, current.URL)
			if e != nil || candidateRoute.key() == original.key() {
				continue
			}
			res, cached := results[candidateRoute.key()]
			if !cached {
				g.pending[name] = pendingCandidate{ID: id, Name: candidate, Route: candidateRoute}
				st.Note = "check next candidate in next one-second batch"
				break
			}
			if res.Health == controlError {
				st.Note = "candidate control error; keep selection"
				break
			}
			g.cursors[name] = cursor{ID: id, Last: candidate}
			if res.Health != healthy {
				continue
			}
			s, st.Note = g.commit(ctx, s, name, current, candidate, candidateRoute, res, st)
			break
		}
	}
	// Report the effective leaf after inner groups have changed.
	g.status.Groups = nil
	for _, name := range names {
		st := groups[name]
		st.Selected = s.Proxies[name].Now
		if r, e := s.resolve(name, g.cfg.ProbeURL); e == nil {
			st.Node = r.Node.Name
			if res, ok := results[r.key()]; ok {
				st.Result = res
			}
		}
		g.status.Groups = append(g.status.Groups, *st)
	}
	for name := range g.cursors {
		if _, ok := groups[name]; !ok {
			delete(g.cursors, name)
			delete(g.pending, name)
		}
	}
}

func (g *Guard) commit(ctx context.Context, s *Snapshot, name string, current Route, candidate string, candidateRoute Route, res Result, st *GroupState) (*Snapshot, string) {
	id := selectionID(s, name, current)
	fresh, e := g.api.Snapshot(ctx)
	if e != nil {
		return s, "cannot revalidate selection"
	}
	freshRoute, e := fresh.resolve(name, g.cfg.ProbeURL)
	if e != nil || selectionID(fresh, name, freshRoute) != id {
		return fresh, "selection changed; discard old result"
	}
	freshCandidate, e := fresh.resolve(candidate, current.URL)
	if e != nil || freshCandidate.key() != candidateRoute.key() || !contains(fresh.Proxies[name].All, candidate) {
		return fresh, "candidate changed; discard old result"
	}
	if g.observe {
		return fresh, "would switch to " + candidate
	}
	putErr := g.api.Select(ctx, name, candidate)
	confirmed, e := g.api.Group(ctx, name)
	if e != nil || confirmed.Now != candidate {
		if putErr != nil {
			return fresh, "switch request failed; not confirmed"
		}
		return fresh, "switch not confirmed"
	}
	old := fresh.Proxies[name].Now
	fresh.Proxies[name] = confirmed
	st.Selected = candidate
	st.Node = candidateRoute.Node.Name
	st.Result = res
	g.status.Switches++
	delete(g.cursors, name)
	g.event("switch group=%q from=%q to=%q node=%q", name, old, candidate, st.Node)
	return fresh, "switched after failed connectivity check"
}
