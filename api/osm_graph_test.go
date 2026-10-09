package api

import (
	"testing"

	"hz-train-map/ingest"
)

func TestBuildOSMGraphAndPathfinding(t *testing.T) {
	// Create a simple test OSM railway geometry track:
	// Node A (45.8, 15.9) -> Node B (45.81, 15.91) -> Node C (45.82, 15.92)
	ways := []ingest.OSMWay{
		{
			ID: 101,
			Geometry: []ingest.OSMNode{
				{Lat: 45.80, Lon: 15.90},
				{Lat: 45.81, Lon: 15.91},
				{Lat: 45.82, Lon: 15.92},
			},
		},
	}

	graph := BuildOSMGraph(ways)
	if graph == nil {
		t.Fatalf("Expected non-nil graph")
	}

	path := graph.FindShortestPath(45.80, 15.90, 45.82, 15.92)
	if len(path) < 3 {
		t.Fatalf("Expected path with at least 3 waypoints along OSM track, got %d", len(path))
	}

	// Verify path start and end
	if mathAbs(path[0].Lat-45.80) > 0.01 || mathAbs(path[0].Lon-15.90) > 0.01 {
		t.Errorf("Path start mismatch: got %v", path[0])
	}
	if mathAbs(path[len(path)-1].Lat-45.82) > 0.01 || mathAbs(path[len(path)-1].Lon-15.92) > 0.01 {
		t.Errorf("Path end mismatch: got %v", path[len(path)-1])
	}
}

func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
