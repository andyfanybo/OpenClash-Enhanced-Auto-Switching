package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type fakeAPI struct {
	mu        sync.Mutex
	s         *Snapshot
	health    map[string]string
	probes    map[string]int
	switches  []string
	snapshots int
	changeAt  int
	change    func(*Snapshot)
	selectErr bool
}

func fixture(groups map[string]Proxy) *fakeAPI {
	nodes := map[string]Node{}
	for _, name := range []string{"a", "b", "c"} {
		nodes[name] = Node{Proxy: Proxy{Name: name, Type: "Shadowsocks"}}
	}
	nodes["DIRECT"] = Node{Proxy: Proxy{Name: "DIRECT", Type: "Direct"}}
	return &fakeAPI{s: &Snapshot{Proxies: groups, Nodes: nodes}, health: map[string]string{}, probes: map[string]int{}}
}

func group(now string, all ...string) Proxy { return Proxy{Type: "Selector", Now: now, All: all} }
func (f *fakeAPI) Snapshot(context.Context) (*Snapshot, error) {
	f.snapshots++
	if f.snapshots == f.changeAt {
		f.change(f.s)
	}
	b, _ := json.Marshal(f.s)
	var s Snapshot
	_ = json.Unmarshal(b, &s)
	return &s, nil
}
func (f *fakeAPI) Probe(_ context.Context, r Route) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes[r.Node.Name]++
	h := f.health[r.Node.Name]
	if h == "" {
		h = healthy
	}
	return Result{Health: h, DelayMS: 100}
}
func (f *fakeAPI) Select(_ context.Context, name, node string) error {
	f.switches = append(f.switches, name+":"+node)
	p := f.s.Proxies[name]
	p.Now = node
	f.s.Proxies[name] = p
	if f.selectErr {
		return fmt.Errorf("response lost")
	}
	return nil
}
func (f *fakeAPI) Group(_ context.Context, name string) (Proxy, error) { return f.s.Proxies[name], nil }
func testGuard(f *fakeAPI) *Guard {
	c := defaultConfig()
	c.StatusFile = ""
	return newGuard(c, f, false)
}

func TestHealthyOnlyChecksCurrentAndDeduplicates(t *testing.T) {
	f := fixture(map[string]Proxy{"节点选择": group("a", "a", "b"), "AI": group("a", "a", "c")})
	g := testGuard(f)
	g.cycle(context.Background())
	if f.probes["a"] != 1 || len(f.probes) != 1 || len(f.switches) != 0 {
		t.Fatalf("probes=%v switches=%v", f.probes, f.switches)
	}
}

func TestSingleFailureSwitchesNextAndReadsBack(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b", "c")})
	f.health["a"] = unavailable
	g := testGuard(f)
	g.cycle(context.Background())
	if len(f.switches) != 0 || f.probes["b"] != 0 {
		t.Fatal("candidate must join the next batch")
	}
	g.cycle(context.Background())
	if strings.Join(f.switches, ",") != "g:b" || f.probes["c"] != 0 || g.status.Switches != 1 {
		t.Fatalf("%+v probes=%v switches=%v", g.status, f.probes, f.switches)
	}
}

func TestNextDeadCandidateContinuesFollowingRound(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b", "c")})
	f.health["a"] = unavailable
	f.health["b"] = unavailable
	g := testGuard(f)
	g.cycle(context.Background())
	if len(f.switches) != 0 || f.probes["c"] != 0 {
		t.Fatal("must not scan all candidates")
	}
	g.cycle(context.Background())
	if len(f.switches) != 0 || f.probes["c"] != 0 {
		t.Fatal("only one pending candidate per round")
	}
	g.cycle(context.Background())
	if strings.Join(f.switches, ",") != "g:c" {
		t.Fatal(f.switches)
	}
}

func TestAllDownDoesNotBlindlySwitch(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b", "c")})
	f.health = map[string]string{"a": unavailable, "b": unavailable, "c": unavailable}
	g := testGuard(f)
	for i := 0; i < 5; i++ {
		g.cycle(context.Background())
	}
	if len(f.switches) != 0 || f.probes["c"] == 0 {
		t.Fatalf("probes=%v switches=%v", f.probes, f.switches)
	}
}

func TestNestedGroupSwitchesChildWithoutChangingParent(t *testing.T) {
	f := fixture(map[string]Proxy{"region": group("a", "a", "b"), "AI": group("region", "region", "c")})
	f.health["a"] = unavailable
	g := testGuard(f)
	g.cycle(context.Background())
	g.cycle(context.Background())
	if strings.Join(f.switches, ",") != "region:b" || f.probes["a"] != 2 || f.s.Proxies["AI"].Now != "region" {
		t.Fatalf("%v %v", f.probes, f.switches)
	}
	for _, s := range g.status.Groups {
		if s.Node != "b" || s.Health != healthy {
			t.Fatal(s)
		}
	}
}

func TestManualOrAutomaticChangeIsFollowedNextRound(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b", "c")})
	g := testGuard(f)
	g.cycle(context.Background())
	p := f.s.Proxies["g"]
	p.Now = "c"
	f.s.Proxies["g"] = p
	g.cycle(context.Background())
	if f.probes["c"] != 1 || f.probes["b"] != 0 {
		t.Fatal(f.probes)
	}
}

func TestChangeDuringProbeDiscardsFailure(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b", "c")})
	f.health["a"] = unavailable
	f.changeAt = 3
	f.change = func(s *Snapshot) { p := s.Proxies["g"]; p.Now = "c"; s.Proxies["g"] = p }
	g := testGuard(f)
	g.cycle(context.Background())
	g.cycle(context.Background())
	if len(f.switches) != 0 {
		t.Fatal(f.switches)
	}
}

func TestRemovedCandidateDiscardsResult(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b", "c")})
	f.health["a"] = unavailable
	f.changeAt = 3
	f.change = func(s *Snapshot) { p := s.Proxies["g"]; p.All = []string{"a", "c"}; s.Proxies["g"] = p }
	g := testGuard(f)
	g.cycle(context.Background())
	g.cycle(context.Background())
	if len(f.switches) != 0 {
		t.Fatal(f.switches)
	}
}

func TestControlErrorDoesNotTriggerFailover(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b")})
	f.health["a"] = controlError
	testGuard(f).cycle(context.Background())
	if len(f.switches) != 0 || f.probes["b"] != 0 {
		t.Fatal(f.probes, f.switches)
	}
}

func TestDirectAndRejectAreNotProbedOrSelected(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "DIRECT", "b"), "direct": group("DIRECT", "DIRECT", "a")})
	f.health["a"] = unavailable
	g := testGuard(f)
	g.cycle(context.Background())
	g.cycle(context.Background())
	if strings.Join(f.switches, ",") != "g:b" || f.probes["DIRECT"] != 0 {
		t.Fatal(f.switches, f.probes)
	}
}

func TestReadBackHandlesLostPUTResponse(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b")})
	f.health["a"] = unavailable
	f.selectErr = true
	g := testGuard(f)
	g.cycle(context.Background())
	g.cycle(context.Background())
	if g.status.Switches != 1 {
		t.Fatal(g.status)
	}
}

func TestObserveDoesNotPUT(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b")})
	f.health["a"] = unavailable
	g := testGuard(f)
	g.observe = true
	g.cycle(context.Background())
	g.cycle(context.Background())
	if len(f.switches) != 0 {
		t.Fatal(f.switches)
	}
}

func TestGroupURLAndCycles(t *testing.T) {
	f := fixture(map[string]Proxy{"outer": group("region", "region"), "region": {Type: "URLTest", Now: "a", All: []string{"a", "b"}, TestURL: "https://check.example/204"}})
	r, err := f.s.resolve("outer", "https://default.example/")
	if err != nil || r.URL != "https://check.example/204" {
		t.Fatal(r, err)
	}
	f.s.Proxies["region"] = group("outer", "outer")
	if _, err = f.s.resolve("outer", ""); err == nil {
		t.Fatal("must reject cycles")
	}
}

func TestClientProviderDiscoveryEncodingAndStatusClasses(t *testing.T) {
	node := "香港 / #1"
	provider := "机场 / 1"
	status := 200
	var requestURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("auth missing")
		}
		switch r.URL.Path {
		case "/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]Proxy{"g": group(node, node)}})
		case "/providers/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{provider: map[string]any{"vehicleType": "HTTP", "proxies": []Proxy{{Name: node, Type: "Trojan"}}}}})
		default:
			requestURI = r.RequestURI
			w.WriteHeader(status)
			fmt.Fprint(w, `{"delay":120}`)
		}
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.Controller = srv.URL
	c := newClient(cfg, "test-secret")
	s, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.resolve("g", cfg.ProbeURL)
	if err != nil {
		t.Fatal(err)
	}
	if r.Node.Provider != provider {
		t.Fatal(r)
	}
	for _, tt := range []struct {
		code   int
		health string
	}{{200, healthy}, {503, unavailable}, {504, unavailable}, {401, controlError}, {404, controlError}, {500, controlError}} {
		status = tt.code
		if got := c.Probe(context.Background(), r); got.Health != tt.health {
			t.Fatalf("%d %+v", tt.code, got)
		}
	}
	want := "/providers/proxies/" + url.PathEscape(provider) + "/" + url.PathEscape(node) + "/healthcheck?"
	if !strings.HasPrefix(requestURI, want) {
		t.Fatalf("bad encoding %s", requestURI)
	}
}

func TestDuplicateProviderNamesAreNotProbed(t *testing.T) {
	f := fixture(map[string]Proxy{"g": group("a", "a", "b")})
	n := f.s.Nodes["a"]
	n.Ambiguous = true
	f.s.Nodes["a"] = n
	testGuard(f).cycle(context.Background())
	if len(f.probes) != 0 || len(f.switches) != 0 {
		t.Fatal(f.probes, f.switches)
	}
}

func TestConfigValidation(t *testing.T) {
	c := defaultConfig()
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.Controller = "http://user:password@localhost:9090"
	if c.validate() == nil {
		t.Fatal("embedded credentials accepted")
	}
	c = defaultConfig()
	c.MaxConcurrent = 0
	if c.validate() == nil {
		t.Fatal("zero concurrency accepted")
	}
}
