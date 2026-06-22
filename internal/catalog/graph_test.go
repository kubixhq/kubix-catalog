//go:build integration

package catalog_test

import (
	"fmt"
	"testing"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
)

func services(names ...string) []catalog.ServiceInfo {
	svcs := make([]catalog.ServiceInfo, len(names))
	for i, n := range names {
		svcs[i] = catalog.ServiceInfo{ServiceName: n, EndpointCount: 1}
	}
	return svcs
}

func dep(from, to string) catalog.Dependency {
	return catalog.Dependency{FromService: from, ToService: to}
}

func findEdge(g *catalog.GraphResponse, from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func hasCycle(g *catalog.GraphResponse, nodes ...string) bool {
	for _, cycle := range g.CircularDependencies {
		if cycleMatches(cycle, nodes) {
			return true
		}
	}
	return false
}

func cycleMatches(cycle []string, want []string) bool {
	if len(cycle) != len(want) {
		return false
	}
	// find start node in cycle
	start := -1
	for i, v := range cycle {
		if v == want[0] {
			start = i
			break
		}
	}
	if start < 0 {
		return false
	}
	for i, w := range want {
		if cycle[(start+i)%len(cycle)] != w {
			return false
		}
	}
	return true
}

// ── Empty / single service ────────────────────────────────────────────────────

func TestGraph_NoServices_EmptyGraph(t *testing.T) {
	g := catalog.BuildGraph(nil, nil)
	if len(g.Nodes) != 0 {
		t.Errorf("nodes = %d, want 0", len(g.Nodes))
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges = %d, want 0", len(g.Edges))
	}
	if len(g.CircularDependencies) != 0 {
		t.Errorf("cycles = %d, want 0", len(g.CircularDependencies))
	}
}

func TestGraph_OneService_NodeNoEdges(t *testing.T) {
	g := catalog.BuildGraph(services("alpha"), nil)
	if len(g.Nodes) != 1 {
		t.Errorf("nodes = %d, want 1", len(g.Nodes))
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges = %d, want 0", len(g.Edges))
	}
	if len(g.CircularDependencies) != 0 {
		t.Errorf("cycles should be 0 for single node")
	}
}

// ── Edges ─────────────────────────────────────────────────────────────────────

func TestGraph_TwoServices_OneEdge(t *testing.T) {
	g := catalog.BuildGraph(services("A", "B"), []catalog.Dependency{dep("A", "B")})
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Errorf("edges = %d, want 1", len(g.Edges))
	}
	if !findEdge(g, "A", "B") {
		t.Error("expected edge A→B")
	}
}

func TestGraph_Chain_A_B_C(t *testing.T) {
	g := catalog.BuildGraph(
		services("A", "B", "C"),
		[]catalog.Dependency{dep("A", "B"), dep("B", "C")},
	)
	if len(g.Edges) != 2 {
		t.Errorf("edges = %d, want 2", len(g.Edges))
	}
	if !findEdge(g, "A", "B") || !findEdge(g, "B", "C") {
		t.Error("expected edges A→B and B→C")
	}
}

func TestGraph_FiveServices_ComplexEdges(t *testing.T) {
	g := catalog.BuildGraph(
		services("A", "B", "C", "D", "E"),
		[]catalog.Dependency{
			dep("A", "B"), dep("A", "C"),
			dep("B", "D"), dep("C", "D"),
			dep("D", "E"),
		},
	)
	if len(g.Edges) != 5 {
		t.Errorf("edges = %d, want 5", len(g.Edges))
	}
	if len(g.CircularDependencies) != 0 {
		t.Errorf("diamond graph has no cycles, got %v", g.CircularDependencies)
	}
}

func TestGraph_Edges_HaveEmptyEndpointsList(t *testing.T) {
	g := catalog.BuildGraph(services("A", "B"), []catalog.Dependency{dep("A", "B")})
	if g.Edges[0].Endpoints == nil {
		t.Error("edge.Endpoints should be empty slice, not nil")
	}
}

// ── Cycle detection ────────────────────────────────────────────────────────────

func TestGraph_Circular_A_B_A(t *testing.T) {
	g := catalog.BuildGraph(
		services("A", "B"),
		[]catalog.Dependency{dep("A", "B"), dep("B", "A")},
	)
	if len(g.CircularDependencies) == 0 {
		t.Error("expected circular dependency A→B→A")
	}
}

func TestGraph_Circular_A_B_C_A(t *testing.T) {
	g := catalog.BuildGraph(
		services("A", "B", "C"),
		[]catalog.Dependency{dep("A", "B"), dep("B", "C"), dep("C", "A")},
	)
	if len(g.CircularDependencies) == 0 {
		t.Error("expected circular dependency A→B→C→A")
	}
}

func TestGraph_SelfLoop_A_A(t *testing.T) {
	g := catalog.BuildGraph(
		services("A"),
		[]catalog.Dependency{dep("A", "A")},
	)
	if len(g.CircularDependencies) == 0 {
		t.Error("expected self-loop A→A to be detected as circular")
	}
}

func TestGraph_NoCycle_Diamond(t *testing.T) {
	// A→B, A→C, B→D, C→D — diamond: no cycle
	g := catalog.BuildGraph(
		services("A", "B", "C", "D"),
		[]catalog.Dependency{dep("A", "B"), dep("A", "C"), dep("B", "D"), dep("C", "D")},
	)
	if len(g.CircularDependencies) != 0 {
		t.Errorf("diamond has no cycle, got %v", g.CircularDependencies)
	}
}

func TestGraph_NoDeps_NoCycles(t *testing.T) {
	g := catalog.BuildGraph(services("A", "B", "C"), nil)
	if len(g.CircularDependencies) != 0 {
		t.Errorf("no deps means no cycles, got %v", g.CircularDependencies)
	}
}

// ── Store → Graph round-trip ──────────────────────────────────────────────────

func TestGraph_StoreIntegration(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc-a", "v1", []catalog.Endpoint{ep("GET", "/a")})
	mustIngest(t, s, "svc-b", "v1", []catalog.Endpoint{ep("GET", "/b")})
	if err := s.SaveDependencies("svc-a", []string{"svc-b"}); err != nil {
		t.Fatalf("SaveDependencies: %v", err)
	}

	svcs, _ := s.AllServicesForGraph()
	deps, _ := s.AllDependencies()
	g := catalog.BuildGraph(svcs, deps)

	if len(g.Nodes) < 2 {
		t.Errorf("expected at least 2 nodes, got %d", len(g.Nodes))
	}
	if !findEdge(g, "svc-a", "svc-b") {
		t.Error("expected edge svc-a→svc-b")
	}
}

func TestGraph_100Services_CompletesQuickly(t *testing.T) {
	svcs := make([]catalog.ServiceInfo, 100)
	deps := make([]catalog.Dependency, 99)
	for i := range svcs {
		svcs[i] = catalog.ServiceInfo{ServiceName: fmt.Sprintf("svc-%d", i), EndpointCount: 5}
		if i > 0 {
			deps[i-1] = dep(fmt.Sprintf("svc-%d", i-1), fmt.Sprintf("svc-%d", i))
		}
	}
	g := catalog.BuildGraph(svcs, deps)
	if len(g.Nodes) != 100 {
		t.Errorf("nodes = %d, want 100", len(g.Nodes))
	}
}
