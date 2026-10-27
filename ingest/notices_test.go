package ingest

import (
	"os"
	"reflect"
	"strings"
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
	items := parseNoticesFromHTML("works", sampleHTML, allStations, validTrains)

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
	items := parseNoticesFromHTML("works", sampleHTML, allStations, validTrains)

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

func TestAccordionContainerIsolation(t *testing.T) {
	htmlSample := `
	<html>
	<body>
		<nav>
			<button>Stanje u prometu - Zagreb GK, Dugo Selo</button>
		</nav>
		<div id="accordionRailwayWorks">
			<button class="accordion">Vrbovec - Križevci</button>
			<div>Zbog radova na pruzi Dugo Selo - Botovo između Vrbovca i Križevaca.</div>
		</div>
	</body>
	</html>
	`

	allStations := []string{"Zagreb GK", "Dugo Selo", "Vrbovec", "Križevci"}
	validTrains := map[string]bool{}
	items := parseNoticesFromHTML("works", htmlSample, allStations, validTrains)

	if len(items) == 0 {
		t.Fatalf("Expected 1 item, got 0")
	}

	for _, st := range items[0].ParsedStations {
		if st == "Dugo Selo" || st == "Zagreb GK" {
			t.Errorf("Station %s from nav button should NOT be extracted", st)
		}
	}
}

// liveFixtureStations includes names that are substrings of words or of other station
// names on the live page ("Osijek" in "kolosijekom", "Sisak" in "Sisak Caprag").
var liveFixtureStations = []string{
	"Karlovac", "Karlovac Centar", "Duga Resa", "Ogulin", "Oštarije", "Split", "Split Predgrađe",
	"Dujmovača", "Perković", "Knin", "Osijek", "Oriovac", "Sibinj", "Sisak", "Sisak Caprag", "Sunja",
	"Novska", "Koprivnica", "Bučje-Koprivnica", "Pleternica", "Nova Kapela-Batrina", "Vinkovci",
	"Županja", "Vrbovec", "Križevci", "Repinec", "Kloštar", "Vojakovački Kloštar", "Varaždin",
	"Golubovec", "Zabok", "Daruvar", "Hrvatski Leskovac", "Jastrebarsko", "Draganići", "Banova Jaruga",
	"Pčelić", "Virovitica", "Hum-Lug", "Gornja Stubica", "Kamanje", "Ozalj", "Buzet", "Lupoglav", "Pazin",
	"Pula",
}

func parseLiveFixture(t *testing.T, category string) []NoticeItem {
	t.Helper()
	html, err := os.ReadFile("testdata/hzpp_works_2026-10-27.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// 701, 480 and 115 are the PDF object numbers that used to leak into every notice.
	validTrains := map[string]bool{}
	for _, tr := range []string{"701", "480", "115", "520", "521", "2702", "2705", "5500", "5003", "5004", "5201", "6602", "6604", "6607", "6609"} {
		validTrains[tr] = true
	}
	return parseNoticesFromHTML(category, string(html), liveFixtureStations, validTrains)
}

func findNotice(t *testing.T, items []NoticeItem, titlePrefix string) NoticeItem {
	t.Helper()
	for _, n := range items {
		if strings.HasPrefix(n.Title, titlePrefix) {
			return n
		}
	}
	t.Fatalf("notice %q not found", titlePrefix)
	return NoticeItem{}
}

func TestLiveFixtureContentExcludesEmbeddedJSONAndFooter(t *testing.T) {
	items := parseLiveFixture(t, "works")
	for _, n := range items {
		for _, noise := range []string{"stationsHR", "{{", "}}", "HŽ Putnički prijevoz društvo", "kolačić"} {
			if strings.Contains(n.Content, noise) {
				t.Errorf("%s: content contains %q", n.Title, noise)
			}
		}
	}

	// The embedded block must still feed station matching.
	n := findNotice(t, items, "Banova Jaruga - Daruvar - Virovitica")
	for _, st := range []string{"Pakrac", "Suhopolje"} {
		if !contains(n.ParsedStations, st) {
			t.Errorf("expected embedded station %s in %v", st, n.ParsedStations)
		}
	}
	// "(do 12.12.)" is open-ended: active from now until 12 December, not the embedded block's range.
	if n.DateFrom != "" || n.DateTo != "2026-12-12" || len(n.DatesJSON) != 0 {
		t.Errorf("dates: from=%q to=%q list=%v, want open-ended until 2026-12-12", n.DateFrom, n.DateTo, n.DatesJSON)
	}
}

func TestLiveFixtureTrains(t *testing.T) {
	items := parseLiveFixture(t, "works")
	expected := map[string][]string{
		"Vinkovci - Županja (19.10. - 6.11.)":        {"2702", "2705"},
		"Novska - Sunja (2. - 30.11.)":               {"5003", "5004", "5201"},
		"Split Predgrađe - Dujmovača (27. - 30.10.)": {"5500"},
		"Karlovac - Duga Resa - Ogulin":              nil,
		"Sisak Caprag - Sunja - Novska":              nil,
	}
	for title, want := range expected {
		if got := findNotice(t, items, title).ParsedTrains; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: trains = %v, want %v", title, got, want)
		}
	}
}

func TestLiveFixtureStations(t *testing.T) {
	items := parseLiveFixture(t, "works")

	cases := []struct {
		title   string
		want    []string
		notWant []string
	}{
		{"Sibinj - Oriovac", []string{"Oriovac", "Sibinj"}, []string{"Osijek"}},
		{"Vrbovec - Križevci (12.10. - 11.11.)", []string{"Križevci", "Repinec", "Vrbovec"}, []string{"Osijek"}},
		{"Sisak Caprag - Sunja - Novska", []string{"Sisak Caprag", "Sunja", "Novska"}, []string{"Sisak"}},
		{"Nova Kapela-Batrina - Pleternica", []string{"Bučje-Koprivnica", "Nova Kapela-Batrina", "Pleternica"}, []string{"Koprivnica"}},
		{"Split Predgrađe - Dujmovača", []string{"Split Predgrađe", "Dujmovača"}, []string{"Split", "Knin", "Oštarije"}},
		{"Vrbovec - Križevci - Koprivnica - Novo Drnje (13.", []string{"Vojakovački Kloštar", "Koprivnica"}, []string{"Kloštar"}},
	}
	for _, c := range cases {
		n := findNotice(t, items, c.title)
		for _, st := range c.want {
			if !contains(n.ParsedStations, st) {
				t.Errorf("%s: expected station %s in %v", c.title, st, n.ParsedStations)
			}
		}
		for _, st := range c.notWant {
			if contains(n.ParsedStations, st) {
				t.Errorf("%s: station %s should not be in %v", c.title, st, n.ParsedStations)
			}
		}
	}
}

func TestLiveFixtureDates(t *testing.T) {
	items := parseLiveFixture(t, "works")

	cases := []struct {
		title    string
		from, to string
		count    int
		include  []string
		exclude  []string
	}{
		// "od 19. do 30. listopada 2026., osim subotom, nedjeljom te 26. listopada"
		{"Karlovac - Duga Resa - Ogulin", "2026-10-19", "2026-10-30", 9,
			[]string{"2026-10-23", "2026-10-27"}, []string{"2026-10-24", "2026-10-25", "2026-10-26"}},
		// "od 19. listopada do 6. studenoga 2026., osim subotom i nedjeljom"
		{"Vinkovci - Županja (19.10. - 6.11.)", "2026-10-19", "2026-11-06", 15,
			[]string{"2026-10-27", "2026-11-02"}, []string{"2026-10-31", "2026-11-01"}},
		// "od 12. listopada u 9.00 sati do 11. studenoga 2026."
		{"Vrbovec - Križevci (12.10. - 11.11.)", "2026-10-12", "2026-11-11", 31,
			[]string{"2026-10-27"}, nil},
		// The weekday exception applies only to the first range, not to "od 21. studenoga ... do 5. prosinca"
		{"Križevci - Koprivnica - Kloštar - Virovitica", "2026-11-02", "2026-12-05", 29,
			[]string{"2026-11-21", "2026-11-22"}, []string{"2026-11-07", "2026-11-18"}},
		// "osim nedjeljom te 18. studenoga"
		{"Varaždin - Golubovec", "2026-11-09", "2026-12-12", 29,
			[]string{"2026-11-14"}, []string{"2026-11-15", "2026-11-18"}},
		// Bus timetable "Zabok 12.12." is a time, not 12 December
		{"Konjščina - Budinščina", "2026-11-02", "2026-11-12", 9,
			[]string{"2026-11-09"}, []string{"2026-11-07", "2026-12-12"}},
		{"Sibinj - Oriovac", "2026-10-27", "2026-10-27", 1, nil, nil},
		// Open-ended: "do 12. prosinca 2026."
		{"Banova Jaruga - Daruvar - Virovitica", "", "2026-12-12", 0, nil, nil},
	}
	for _, c := range cases {
		n := findNotice(t, items, c.title)
		if n.DateFrom != c.from || n.DateTo != c.to || len(n.DatesJSON) != c.count {
			t.Errorf("%s: got %s..%s (%d days), want %s..%s (%d days): %v", c.title, n.DateFrom, n.DateTo, len(n.DatesJSON), c.from, c.to, c.count, n.DatesJSON)
		}
		for _, d := range c.include {
			if !contains(n.DatesJSON, d) {
				t.Errorf("%s: expected %s in %v", c.title, d, n.DatesJSON)
			}
		}
		for _, d := range c.exclude {
			if contains(n.DatesJSON, d) {
				t.Errorf("%s: %s should not be in %v", c.title, d, n.DatesJSON)
			}
		}
	}
}

func TestIdenticalInfoAndWorksPagesAreDeduplicated(t *testing.T) {
	works := parseLiveFixture(t, "works")
	all := appendUniqueNotices(nil, works)
	all = appendUniqueNotices(all, parseLiveFixture(t, "info"))

	if len(all) != len(works) {
		t.Fatalf("expected %d notices after dedup, got %d", len(works), len(all))
	}
	for _, n := range all {
		if n.Category != "works" {
			t.Errorf("notice %s kept category %q, want works", n.ID, n.Category)
		}
	}
}

func TestParseDatesFromTitle(t *testing.T) {
	year := "2026"
	cases := []struct {
		title string
		want  []string
	}{
		{"(13. - 16. i 27. - 30.11.)", []string{"11-13", "11-14", "11-15", "11-16", "11-27", "11-28", "11-29", "11-30"}},
		{"(29. i 30.10.)", []string{"10-29", "10-30"}},
		{"(2.11. - 5.12.)", nil},
	}
	for _, c := range cases {
		dates, _, _ := parseDatesFromText(c.title+" "+year+".", true)
		if c.want == nil {
			if len(dates) != 34 || dates[0] != "2026-11-02" || dates[33] != "2026-12-05" {
				t.Errorf("%s: got %v", c.title, dates)
			}
			continue
		}
		var want []string
		for _, d := range c.want {
			want = append(want, year+"-"+d)
		}
		if !reflect.DeepEqual(dates, want) {
			t.Errorf("%s: got %v, want %v", c.title, dates, want)
		}
	}

	if dates, from, to := parseDatesFromText("Bubnjarci - Karlovac (do 12.12.)", true); dates != nil || from != "" || !strings.HasSuffix(to, "-12-12") {
		t.Errorf("open-ended title: got %v %q %q", dates, from, to)
	}
}

func TestLiveFixtureSections(t *testing.T) {
	items := parseLiveFixture(t, "works")

	cases := []struct {
		title    string
		sections [][]string
		line     []string
	}{
		// "između kolodvora Duga Resa i Oštarije"
		{"Karlovac - Duga Resa - Ogulin", [][]string{{"Duga Resa", "Oštarije"}}, nil},
		// Two sections: "... Hrvatski Leskovac i Jastrebarsko ... te između kolodvora Jastrebarsko i Draganići"
		{"Hrvatski Leskovac - Jastrebarsko", [][]string{{"Hrvatski Leskovac", "Jastrebarsko"}, {"Jastrebarsko", "Draganići"}}, nil},
		// No "između": the section is the line, "pruga Varaždin – Koprivnica bit će zatvorena za promet od 9."
		{"Varaždin - Koprivnica (9. - 30.10.)", [][]string{{"Varaždin", "Koprivnica"}}, nil},
		// Section names more than the closed line "pruga Banova Jaruga – Pčelić"
		{"Banova Jaruga - Daruvar - Virovitica", [][]string{{"Banova Jaruga", "Daruvar", "Virovitica"}}, []string{"Banova Jaruga", "Pčelić"}},
		// "između kolodvora (Zabok) – Hum Lug – Gornja Stubica", GTFS spells it "Hum-Lug"
		{"Zabok - Gornja Stubica", [][]string{{"Zabok", "Hum-Lug", "Gornja Stubica"}}, []string{"Hum-Lug", "Gornja Stubica"}},
		{"Buzet - Lupoglav - Pazin (5. - 26.10.)", [][]string{{"Buzet", "Lupoglav", "Pazin"}}, []string{"Buzet", "Pula"}},
		{"Bubnjarci - Karlovac - Duga Resa", [][]string{{"Karlovac", "Kamanje"}}, []string{"Karlovac", "Ozalj", "Kamanje"}},
		// "između kolodvora Križevci i Gyekenyes": Gyekenyes is not a GTFS station
		{"Vrbovec - Križevci - Koprivnica - Novo Drnje (13.", nil, nil},
	}
	for _, c := range cases {
		n := findNotice(t, items, c.title)
		if !reflect.DeepEqual(n.SectionStations, c.sections) {
			t.Errorf("%s: sections = %v, want %v", c.title, n.SectionStations, c.sections)
		}
		if c.line != nil && !reflect.DeepEqual(n.LineStations, c.line) {
			t.Errorf("%s: line = %v, want %v", c.title, n.LineStations, c.line)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
