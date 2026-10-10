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

func TestCulicSopnicaDirectPath(t *testing.T) {
	// Way 1 (Direct main line, shorter ~2.1 km)
	// Way 2 (Freight loop, longer ~4.5 km with slightly closer candidate node)
	ways := []ingest.OSMWay{
		{
			ID: 101, // Direct main line
			Geometry: []ingest.OSMNode{
				{Lat: 45.82084, Lon: 16.06361},
				{Lat: 45.82270, Lon: 16.07450},
				{Lat: 45.82457, Lon: 16.08579},
			},
		},
		{
			ID: 102, // Freight loop
			Geometry: []ingest.OSMNode{
				{Lat: 45.82084, Lon: 16.06361},
				{Lat: 45.81500, Lon: 16.07000},
				{Lat: 45.82448, Lon: 16.08578},
			},
		},
	}

	graph := BuildOSMGraph(ways)
	path := graph.FindShortestPath(45.82084, 16.06361, 45.82457, 16.08579)

	if len(path) == 0 {
		t.Fatalf("Expected non-empty path")
	}

	// Verify path does not divert south to lat 45.8150
	for _, pt := range path {
		if pt.Lat < 45.818 {
			t.Errorf("Path diverted too far south to freight loop: lat %f", pt.Lat)
		}
	}
}
