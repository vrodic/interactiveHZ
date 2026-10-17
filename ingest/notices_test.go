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
	validTrains := map[string]bool{"4600": true, "4603": true}
	items := parseNoticesFromHTML("works", sampleHTML, allStations, validTrains, nil)

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

	if len(item.DatesJSON) == 0 {
		t.Errorf("Expected extracted dates in DatesJSON, got empty")
	}
}

func TestLineNamePrefixStripping(t *testing.T) {
	sampleHTML := `
	<button class="accordion">Zagreb Borongaj - Zagreb GK (17. i 18.10.)</button>
	<div>
		Zbog radova na pruzi Zagreb GK – Dugo Selo između kolodvora Zagreb Borongaj i Zagreb GK 17. i 18. listopada 2026. od 7.50 do 16.30 sati promet će teći desnim kolosijekom.
	</div>
	`

	allStations := []string{"Zagreb GK", "Zagreb Borongaj", "Dugo Selo", "Vrbovec", "Križevci"}
	validTrains := map[string]bool{}
	items := parseNoticesFromHTML("works", sampleHTML, allStations, validTrains, nil)

	if len(items) == 0 {
		t.Fatalf("Expected parsed notice items, got 0")
	}

	item := items[0]
	for _, st := range item.ParsedStations {
		if st == "Dugo Selo" {
			t.Errorf("Dugo Selo should NOT be extracted as an affected station when named only in line description 'na pruzi Zagreb GK – Dugo Selo'")
		}
	}

	foundBorongaj := false
	for _, st := range item.ParsedStations {
		if st == "Zagreb Borongaj" {
			foundBorongaj = true
		}
	}
	if !foundBorongaj {
		t.Errorf("Expected Zagreb Borongaj to be extracted as affected station, got %v", item.ParsedStations)
	}
}
