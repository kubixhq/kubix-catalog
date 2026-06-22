package catalog

func BuildGraph(services []ServiceInfo, deps []Dependency) *GraphResponse {
	nodes := make([]GraphNode, 0, len(services))
	for _, svc := range services {
		nodes = append(nodes, GraphNode{
			ServiceName:   svc.ServiceName,
			EndpointCount: svc.EndpointCount,
		})
	}

	edges := make([]GraphEdge, 0, len(deps))
	for _, d := range deps {
		edges = append(edges, GraphEdge{
			From:      d.FromService,
			To:        d.ToService,
			Endpoints: []string{},
		})
	}

	cycles := detectCycles(edges)

	return &GraphResponse{
		Nodes:                nodes,
		Edges:                edges,
		CircularDependencies: cycles,
	}
}

func detectCycles(edges []GraphEdge) [][]string {
	adj := make(map[string][]string)
	nodes := make(map[string]bool)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		nodes[e.From] = true
		nodes[e.To] = true
	}

	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	var cycles [][]string
	seen := make(map[string]bool) // dedup identical cycles

	var dfs func(node string, path []string)
	dfs = func(node string, path []string) {
		visited[node] = true
		inStack[node] = true
		path = append(path, node)

		for _, neighbor := range adj[node] {
			if !visited[neighbor] {
				dfs(neighbor, path)
			} else if inStack[neighbor] {
				start := -1
				for i, p := range path {
					if p == neighbor {
						start = i
						break
					}
				}
				if start >= 0 {
					cycle := make([]string, len(path)-start)
					copy(cycle, path[start:])
					key := cycleKey(cycle)
					if !seen[key] {
						seen[key] = true
						cycles = append(cycles, cycle)
					}
				}
			}
		}

		inStack[node] = false
	}

	for node := range nodes {
		if !visited[node] {
			dfs(node, []string{})
		}
	}

	if cycles == nil {
		cycles = [][]string{}
	}
	return cycles
}

func cycleKey(cycle []string) string {
	// Canonical form: rotate so the lexicographically smallest node is first
	if len(cycle) == 0 {
		return ""
	}
	minIdx := 0
	for i, v := range cycle {
		if v < cycle[minIdx] {
			minIdx = i
		}
	}
	var sb string
	for i := range cycle {
		sb += cycle[(minIdx+i)%len(cycle)] + "→"
	}
	return sb
}
