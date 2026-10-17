package ingest

import (
	"testing"
)

func TestParseNoticesSampleHTML(t *testing.T) {
	sampleHTML := `
	<button class="accordion">Zagreb Borongaj - Zagreb GK (17. i 18.10.)</button>
	<div>
		Zbog radova na pruzi Zagreb GK – Dugo Selo između kolodvora Zagreb Borongaj i Zagreb GK 17. i 18. listopada 2026. od 7.50 do 16.30 sati promet će teći desnim kolosijekom te se očekuju kašnjenja.
		Vlakovi 4600 i 4603 voze umjesto vlakova autobusi.
		<a href="https://www.hzpp.hr/api/v1/repository/download/43b59547-57c1-4dab-bd66-83518c39ec87">Popis vlakova</a>
	</div>
	`

	allStations := []string{"Zagreb GK", "Zagreb Borongaj", "Dugo Selo", "Rijeka", "Split"}
	items := parseNoticesFromHTML("works", sampleHTML, allStations, nil)

	if len(items) == 0 {
		t.Fatalf("Expected parsed notice items, got 0")
	}

	item := items[0]
	if !item.IsTrackWorks {
		t.Errorf("Expected IsTrackWorks=true, got false")
	}
	if !item.IsBusReplacement {
		t.Errorf("Expected IsBusReplacement=true, got false")
	}

	foundGK := false
	for _, st := range item.ParsedStations {
		if st == "Zagreb GK" {
			foundGK = true
		}
	}
	if !foundGK {
		t.Errorf("Expected Zagreb GK in parsed stations, got %v", item.ParsedStations)
	}

	found4600 := false
	for _, tr := range item.ParsedTrains {
		if tr == "4600" {
			found4600 = true
		}
	}
	if !found4600 {
		t.Errorf("Expected train 4600 in parsed trains, got %v", item.ParsedTrains)
	}

	if len(item.PDFURLs) != 1 {
		t.Errorf("Expected 1 PDF URL, got %d", len(item.PDFURLs))
	}
}
