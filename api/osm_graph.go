package api

import (
	"container/heap"
	"fmt"
	"math"
	"sort"

	"hz-train-map/ingest"
)

type nodeID int

type GraphNode struct {
	id  nodeID
	lat float64
	lon float64
}

type GraphEdge struct {
	to     nodeID
	distKm float64
}

type OSMGraph struct {
	nodes map[nodeID]GraphNode
	edges map[nodeID][]GraphEdge
}

func nodeKey(lat, lon float64) string {
	return fmt.Sprintf("%.5f,%.5f", lat, lon)
}

func BuildOSMGraph(ways []ingest.OSMWay) *OSMGraph {
	graph := &OSMGraph{
		nodes: make(map[nodeID]GraphNode),
		edges: make(map[nodeID][]GraphEdge),
	}

	keyToID := make(map[string]nodeID)
	nextID := nodeID(1)

	getOrCreateID := func(node ingest.OSMNode) nodeID {
		key := nodeKey(node.Lat, node.Lon)
		if id, exists := keyToID[key]; exists {
			return id
		}
		id := nextID
		nextID++
		keyToID[key] = id
		graph.nodes[id] = GraphNode{id: id, lat: node.Lat, lon: node.Lon}
		return id
	}

	for _, way := range ways {
		if len(way.Geometry) < 2 {
			continue
		}
		for i := 0; i < len(way.Geometry)-1; i++ {
			n1 := way.Geometry[i]
			n2 := way.Geometry[i+1]

			id1 := getOrCreateID(n1)
			id2 := getOrCreateID(n2)

			dist := haversineKm(n1.Lat, n1.Lon, n2.Lat, n2.Lon)

			graph.edges[id1] = append(graph.edges[id1], GraphEdge{to: id2, distKm: dist})
			graph.edges[id2] = append(graph.edges[id2], GraphEdge{to: id1, distKm: dist})
		}
	}

	return graph
}

func (g *OSMGraph) FindNearestNode(lat, lon float64, maxDistKm float64) (nodeID, float64) {
	var bestID nodeID
	bestDist := math.MaxFloat64

	for id, node := range g.nodes {
		d := haversineKm(lat, lon, node.lat, node.lon)
		if d < bestDist {
			bestDist = d
			bestID = id
		}
	}

	if bestDist <= maxDistKm {
		return bestID, bestDist
	}
	return 0, bestDist
}

func (g *OSMGraph) FindCandidateNodes(lat, lon float64, maxDistKm float64, limit int) []nodeID {
	type cand struct {
		id   nodeID
		dist float64
	}
	var cands []cand

	for id, node := range g.nodes {
		d := haversineKm(lat, lon, node.lat, node.lon)
		if d <= maxDistKm {
			cands = append(cands, cand{id: id, dist: d})
		}
	}

	sort.Slice(cands, func(i, j int) bool {
		return cands[i].dist < cands[j].dist
	})

	if len(cands) > limit {
		cands = cands[:limit]
	}

	res := make([]nodeID, len(cands))
	for i, c := range cands {
		res[i] = c.id
	}
	return res
}

type pathItem struct {
	id     nodeID
	cost   float64
	est    float64
	index  int
}

type priorityQueue []*pathItem

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return (pq[i].cost + pq[i].est) < (pq[j].cost + pq[j].est) }
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*pathItem)
	item.index = n
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

func (g *OSMGraph) searchPathBetween(startNodeID, endNodeID nodeID) ([]nodeID, float64) {
	if startNodeID == endNodeID {
		return []nodeID{startNodeID}, 0.0
	}

	targetNode := g.nodes[endNodeID]

	dist := make(map[nodeID]float64)
	prev := make(map[nodeID]nodeID)

	for id := range g.nodes {
		dist[id] = math.MaxFloat64
	}

	dist[startNodeID] = 0.0

	pq := &priorityQueue{}
	heap.Init(pq)

	hStart := haversineKm(g.nodes[startNodeID].lat, g.nodes[startNodeID].lon, targetNode.lat, targetNode.lon)
	heap.Push(pq, &pathItem{id: startNodeID, cost: 0, est: hStart})

	visited := make(map[nodeID]bool)

	found := false
	for pq.Len() > 0 {
		curr := heap.Pop(pq).(*pathItem)
		u := curr.id

		if u == endNodeID {
			found = true
			break
		}

		if visited[u] {
			continue
		}
		visited[u] = true

		for _, edge := range g.edges[u] {
			v := edge.to
			if visited[v] {
				continue
			}

			newDist := dist[u] + edge.distKm
			if newDist < dist[v] {
				dist[v] = newDist
				prev[v] = u
				h := haversineKm(g.nodes[v].lat, g.nodes[v].lon, targetNode.lat, targetNode.lon)
				heap.Push(pq, &pathItem{id: v, cost: newDist, est: h})
			}
		}
	}

	if !found {
		return nil, math.MaxFloat64
	}

	var pathNodes []nodeID
	curr := endNodeID
	for curr != 0 {
		pathNodes = append([]nodeID{curr}, pathNodes...)
		if curr == startNodeID {
			break
		}
		curr = prev[curr]
	}

	return pathNodes, dist[endNodeID]
}

func (g *OSMGraph) FindShortestPath(fromLat, fromLon, toLat, toLon float64) []LatLon {
	if g == nil || len(g.nodes) == 0 {
		return nil
	}

	startCandidates := g.FindCandidateNodes(fromLat, fromLon, 0.3, 5)
	endCandidates := g.FindCandidateNodes(toLat, toLon, 0.3, 5)

	if len(startCandidates) == 0 {
		startNode, _ := g.FindNearestNode(fromLat, fromLon, 3.0)
		if startNode != 0 {
			startCandidates = []nodeID{startNode}
		}
	}

	if len(endCandidates) == 0 {
		endNode, _ := g.FindNearestNode(toLat, toLon, 3.0)
		if endNode != 0 {
			endCandidates = []nodeID{endNode}
		}
	}

	if len(startCandidates) == 0 || len(endCandidates) == 0 {
		return nil
	}

	var bestPathNodes []nodeID
	bestDist := math.MaxFloat64

	for _, startID := range startCandidates {
		for _, endID := range endCandidates {
			pathNodes, distKm := g.searchPathBetween(startID, endID)
			if len(pathNodes) > 0 && distKm < bestDist {
				bestDist = distKm
				bestPathNodes = pathNodes
			}
		}
	}

	if len(bestPathNodes) == 0 {
		return nil
	}

	waypoints := make([]LatLon, 0, len(bestPathNodes)+2)
	waypoints = append(waypoints, LatLon{Lat: fromLat, Lon: fromLon})

	for _, nid := range bestPathNodes {
		n := g.nodes[nid]
		if len(waypoints) > 0 {
			last := waypoints[len(waypoints)-1]
			if haversineKm(last.Lat, last.Lon, n.lat, n.lon) < 0.01 {
				continue
			}
		}
		waypoints = append(waypoints, LatLon{Lat: n.lat, Lon: n.lon})
	}

	if len(waypoints) > 0 {
		last := waypoints[len(waypoints)-1]
		if haversineKm(last.Lat, last.Lon, toLat, toLon) >= 0.01 {
			waypoints = append(waypoints, LatLon{Lat: toLat, Lon: toLon})
		}
	} else {
		waypoints = append(waypoints, LatLon{Lat: toLat, Lon: toLon})
	}

	return waypoints
}
