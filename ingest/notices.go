package ingest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type NoticeItem struct {
	ID               string     `json:"id"`
	Category         string     `json:"category"` // "info" or "works"
	Title            string     `json:"title"`
	Content          string     `json:"content"`
	ParsedStations   []string   `json:"parsed_stations"`
	SectionStations  [][]string `json:"section_stations,omitempty"`
	LineStations     []string   `json:"line_stations,omitempty"`
	ParsedTrains     []string   `json:"parsed_trains"`
	PDFURLs          []string   `json:"pdf_urls"`
	IsBusReplacement bool       `json:"is_bus_replacement"`
	IsTrackWorks     bool       `json:"is_track_works"`
	DateFrom         string     `json:"date_from,omitempty"`
	DateTo           string     `json:"date_to,omitempty"`
	DatesJSON        []string   `json:"dates_json,omitempty"`
	UpdatedAt        string     `json:"updated_at"`
}

type EmbeddedJSONNotice struct {
	StationsHR []string `json:"stationsHR"`
	Dates      []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"dates"`
}

var (
	noticeHashesMu sync.Mutex
	lastPagesHash  string
)

// noticePages lists the pages scraped for notices. Works comes first so that when
// both pages serve the same accordion (as hzpp.hr currently does) the notices keep
// the "works" category and the duplicates from the info page are dropped.
var noticePages = []struct {
	category string
	url      string
}{
	{"works", "https://www.hzpp.hr/hr/informacije?type=works"},
	{"info", "https://www.hzpp.hr/hr/informacije?type=info"},
}

var (
	trainNumRegex  = regexp.MustCompile(`\b[1-9]\d{2,4}\b`)
	pdfURLRegex    = regexp.MustCompile(`(?i)(?:https?://www\.hzpp\.hr)?/api/v1/repository/download/[a-f0-9\-]+`)
	hrefPDFRegex   = regexp.MustCompile(`(?i)href=["']([^"']+\.pdf)["']`)
	buttonTagRegex = regexp.MustCompile(`(?i)<button[^>]*>`)

	croatianMonths = map[string]string{
		"siječanj": "01", "siječnja": "01", "siječnju": "01",
		"veljača": "02", "veljače": "02", "veljači": "02",
		"ožujak": "03", "ožujka": "03", "ožujku": "03",
		"travanj": "04", "travnja": "04", "travnju": "04",
		"svibanj": "05", "svibnja": "05", "svibnju": "05",
		"lipanj": "06", "lipnja": "06", "lipnju": "06",
		"srpanj": "07", "srpnja": "07", "srpnju": "07",
		"kolovoz": "08", "kolovoza": "08", "kolovozu": "08",
		"rujan": "09", "rujna": "09", "rujnu": "09",
		"listopad": "10", "listopada": "10", "listopadu": "10",
		"studeni": "11", "studenoga": "11", "studenog": "11", "studenom": "11",
		"prosinac": "12", "prosinca": "12", "prosinci": "12",
	}
)

func FetchAndProcessNotices(database *sql.DB, allStations []string) error {
	client := &http.Client{Timeout: 15 * time.Second}

	// Fetch every page before touching the DB: saving a partial result would wipe
	// the notices of the page that failed.
	bodies := make([]string, len(noticePages))
	hasher := sha256.New()
	for i, p := range noticePages {
		body, err := fetchPage(client, p.url)
		if err != nil {
			return fmt.Errorf("failed to fetch %s notices page: %w", p.category, err)
		}
		bodies[i] = body
		hasher.Write([]byte(body))
	}
	hashStr := hex.EncodeToString(hasher.Sum(nil))

	noticeHashesMu.Lock()
	unchanged := hashStr == lastPagesHash
	noticeHashesMu.Unlock()
	if unchanged {
		log.Println("Traffic notices and track works pages unchanged.")
		return nil
	}

	validTrainNums := fetchValidTrainNumbers(database)
	var allNotices []NoticeItem
	for i, p := range noticePages {
		items := parseNoticesFromHTML(p.category, bodies[i], allStations, validTrainNums)
		allNotices = appendUniqueNotices(allNotices, items)
	}

	if err := saveNoticesToDB(database, allNotices); err != nil {
		return fmt.Errorf("failed to save notices to DB: %w", err)
	}

	noticeHashesMu.Lock()
	lastPagesHash = hashStr
	noticeHashesMu.Unlock()

	log.Printf("Successfully ingested %d traffic notices and track works.", len(allNotices))
	return nil
}

// appendUniqueNotices appends items whose title and content are not already present.
func appendUniqueNotices(existing, items []NoticeItem) []NoticeItem {
	seen := make(map[string]bool, len(existing))
	for _, n := range existing {
		seen[n.Title+"\n"+n.Content] = true
	}
	for _, n := range items {
		key := n.Title + "\n" + n.Content
		if seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, n)
	}
	return existing
}

func fetchValidTrainNumbers(database *sql.DB) map[string]bool {
	validMap := make(map[string]bool)
	rows, err := database.Query(`
		SELECT DISTINCT trip_short_name FROM trips WHERE trip_short_name IS NOT NULL AND trip_short_name != ''
		UNION
		SELECT DISTINCT trip_id FROM trips WHERE trip_id IS NOT NULL AND trip_id != ''
	`)
	if err != nil {
		return validMap
	}
	defer rows.Close()

	for rows.Next() {
		var num string
		if err := rows.Scan(&num); err == nil {
			num = strings.TrimSpace(num)
			if num != "" {
				validMap[num] = true
			}
		}
	}
	return validMap
}

func fetchPage(client *http.Client, pageURL string) (string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bodyBytes), nil
}

var accordionContainerRegex = regexp.MustCompile(`(?i)id="accordion[^"]*"`)

// footerRegex marks the start of the site footer, which otherwise ends up in the last notice.
var footerRegex = regexp.MustCompile(`(?i)<!--\s*Footer|<footer[\s>]`)

// embeddedJSONBlockRegex matches the hidden {{ ... }} station/date data block in a notice.
var embeddedJSONBlockRegex = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

func parseNoticesFromHTML(category string, html string, allStations []string, validTrainNums map[string]bool) []NoticeItem {
	var items []NoticeItem

	// Isolate main accordion container to skip navigation/footer noise
	htmlContent := html
	if loc := accordionContainerRegex.FindStringIndex(html); loc != nil {
		htmlContent = html[loc[0]:]
	}
	if loc := footerRegex.FindStringIndex(htmlContent); loc != nil {
		htmlContent = htmlContent[:loc[0]]
	}

	sections := buttonTagRegex.Split(htmlContent, -1)
	if len(sections) <= 1 {
		sections = []string{htmlContent}
	}

	for idx, sec := range sections {
		// The embedded JSON block is machine data (parsed below), not notice text.
		cleanText := stripHTMLTags(embeddedJSONBlockRegex.ReplaceAllString(sec, ""))
		cleanText = strings.ReplaceAll(cleanText, "&nbsp;", " ")
		cleanText = strings.TrimSpace(cleanText)

		if len(cleanText) < 20 {
			continue
		}

		lines := strings.Split(cleanText, "\n")
		var title string
		var contentLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if title == "" {
				title = trimmed
			} else {
				contentLines = append(contentLines, trimmed)
			}
		}

		if title == "" || strings.HasPrefix(title, "http") || strings.Contains(title, "Google Tag Manager") {
			continue
		}

		// Noise title filtering
		titleLower := strings.ToLower(title)
		if strings.Contains(titleLower, "kontakt") ||
			strings.Contains(titleLower, "korisnički centar") ||
			strings.Contains(titleLower, "korisnicki centar") ||
			strings.Contains(titleLower, "prava putnika") ||
			strings.Contains(titleLower, "pritužbe") ||
			strings.Contains(titleLower, "povrat novca") ||
			strings.Contains(titleLower, "prihvati sve") ||
			strings.Contains(titleLower, "prihvaćam") ||
			strings.Contains(titleLower, "slažem se") ||
			strings.Contains(titleLower, "kolačići") ||
			strings.Contains(titleLower, "kolacici") ||
			strings.Contains(titleLower, "cookie") {
			continue
		}

		content := strings.Join(contentLines, "\n")
		fullSectionText := sec + "\n" + cleanText

		pdfURLs := extractPDFURLs(fullSectionText)
		// Train numbers come from the notice text only. The linked PDF train lists use
		// CID-encoded fonts, so scanning their raw bytes only yields PDF object numbers
		// that happen to collide with real train numbers.
		rawTrains := extractTrainNumbers(cleanText)
		stations := extractStations(cleanText, allStations)
		sections := extractSections(content, allStations)
		line := extractLine(content, allStations)

		// Check embedded JSON
		embeddedStations, embeddedFrom, embeddedTo := extractEmbeddedJSONData(sec)
		if len(embeddedStations) > 0 {
			stations = mergeUnique(stations, embeddedStations)
		}

		// Filter trains against valid database GTFS train numbers
		var validTrains []string
		for _, tr := range rawTrains {
			if validTrainNums[tr] {
				validTrains = append(validTrains, tr)
			}
		}

		// The body states the exact days (with exceptions); the title is only a
		// summary such as "(19. - 30.10.)", so it is used when the body has no dates.
		datesList, dateFrom, dateTo := parseDatesFromText(content, false)
		if dateTo == "" {
			datesList, dateFrom, dateTo = parseDatesFromText(title, true)
		}
		if dateTo == "" {
			dateFrom, dateTo = embeddedFrom, embeddedTo
		}

		isBus := strings.Contains(strings.ToLower(fullSectionText), "autobus") ||
			strings.Contains(strings.ToLower(fullSectionText), "zamjensk") ||
			strings.Contains(strings.ToLower(fullSectionText), "minibus")

		isTrackWorks := category == "works" ||
			strings.Contains(strings.ToLower(fullSectionText), "radov") ||
			strings.Contains(strings.ToLower(fullSectionText), "zatvoren") ||
			strings.Contains(strings.ToLower(fullSectionText), "održavanj")

		// Strict filtering: require at least 1 mapped station or train, OR clear bus/works designation
		if len(stations) == 0 && len(validTrains) == 0 {
			continue
		}

		noticeID := fmt.Sprintf("%s-%d-%s", category, idx, hashString(title))

		items = append(items, NoticeItem{
			ID:               noticeID,
			Category:         category,
			Title:            title,
			Content:          content,
			ParsedStations:   stations,
			SectionStations:  sections,
			LineStations:     line,
			ParsedTrains:     validTrains,
			PDFURLs:          pdfURLs,
			IsBusReplacement: isBus,
			IsTrackWorks:     isTrackWorks,
			DateFrom:         dateFrom,
			DateTo:           dateTo,
			DatesJSON:        datesList,
			UpdatedAt:        time.Now().In(time.Local).Format("2006-01-02 15:04:05"),
		})
	}

	return items
}

// dateTokenRegex splits notice text into the pieces the date parser cares about:
// "19.10." (day+month), "9.00" (time of day), "19." (day), bare numbers, words,
// dashes and punctuation.
var dateTokenRegex = regexp.MustCompile(`(\d{1,2})\.(\d{1,2})\.|(\d{1,2})[.:](\d{2})|(\d{1,2})\.|(\d+)|(\p{L}+)|([–—-])|([.,;])`)

var croatianWeekdays = map[string][]time.Weekday{
	"ponedjeljkom": {time.Monday},
	"utorkom":      {time.Tuesday},
	"srijedom":     {time.Wednesday},
	"četvrtkom":    {time.Thursday},
	"petkom":       {time.Friday},
	"subotom":      {time.Saturday},
	"subotama":     {time.Saturday},
	"nedjeljom":    {time.Sunday},
	"nedjeljama":   {time.Sunday},
	"vikendom":     {time.Saturday, time.Sunday},
}

type dateAtom struct {
	day, month, year int
	numeric          bool // written as "12.12." rather than "12. prosinca"
	explicitYear     bool
}

// dateSpan is an inclusive range of atoms; start == -1 means "until end" with no start.
type dateSpan struct {
	start, end int
	closed     bool
}

// dateExclusion is an "osim ..." clause: it removes weekdays and explicit days from
// the spans stated before it (spans[fromSpan:toSpan]).
type dateExclusion struct {
	fromSpan, toSpan int
	weekdays         map[time.Weekday]bool
	atoms            []int
}

// parseDatesFromText extracts the days a notice applies to from Croatian text such as
// "od 19. do 30. listopada 2026., osim subotom, nedjeljom te 26. listopada" or
// "(13. - 16. i 27. - 30.11.)". It returns every affected day, plus the first and last
// day. For an open-ended notice ("do 12. prosinca 2026.") the list and the first day
// are empty and only the last day is set.
//
// Notice bodies also contain bus timetables ("Zabok 12.12."), so unless isTitle is set a
// lone "DD.MM." only counts as a date when it has a year or is part of a range.
func parseDatesFromText(text string, isTitle bool) ([]string, string, string) {
	const (
		tokOther = iota
		tokAtom
		tokMonth
		tokYear
	)

	lower := strings.ToLower(text)
	nowYear := time.Now().Year()

	var atoms []dateAtom
	var spans []dateSpan
	var exclusions []dateExclusion

	pendingOd := false
	openSpan := -1   // span started by "od" still waiting for its "do"
	pendingEnd := -1 // span whose end is the next atom
	pendingUntil := false
	var excl *dateExclusion
	scopeStart := 0
	lastTok := tokOther

	closeExclusion := func() {
		if excl != nil {
			excl.toSpan = len(spans)
			exclusions = append(exclusions, *excl)
			excl = nil
			scopeStart = len(spans)
		}
	}

	propagate := func(month, year int) {
		for i := len(atoms) - 1; i >= 0; i-- {
			changed := false
			if month > 0 && atoms[i].month == 0 {
				atoms[i].month = month
				changed = true
			}
			if year > 0 && atoms[i].year == 0 {
				atoms[i].year = year
				atoms[i].explicitYear = true
				changed = true
			}
			if !changed {
				break
			}
		}
	}

	addAtom := func(day, month int, numeric bool) {
		propagate(month, 0)
		atoms = append(atoms, dateAtom{day: day, month: month, numeric: numeric})
		idx := len(atoms) - 1

		switch {
		case excl != nil:
			excl.atoms = append(excl.atoms, idx)
		case pendingEnd >= 0:
			spans[pendingEnd].end = idx
			spans[pendingEnd].closed = true
		case pendingUntil:
			spans = append(spans, dateSpan{start: -1, end: idx, closed: true})
		default:
			spans = append(spans, dateSpan{start: idx, end: idx})
			if pendingOd {
				openSpan = len(spans) - 1
			}
		}
		pendingOd, pendingEnd, pendingUntil = false, -1, false
	}

	for _, m := range dateTokenRegex.FindAllStringSubmatch(lower, -1) {
		isAtom := m[1] != "" || m[5] != ""
		if !isAtom {
			if pendingEnd >= 0 || pendingUntil {
				openSpan = -1
			}
			pendingOd, pendingEnd, pendingUntil = false, -1, false
		}

		switch {
		case m[1] != "":
			day, _ := strconv.Atoi(m[1])
			month, _ := strconv.Atoi(m[2])
			if day < 1 || day > 31 || month < 1 || month > 12 {
				lastTok = tokOther
				continue
			}
			addAtom(day, month, true)
			lastTok = tokAtom
		case m[3] != "":
			// Time of day, e.g. "od 9.00 do 13.50 sati"
			lastTok = tokOther
		case m[5] != "":
			day, _ := strconv.Atoi(m[5])
			if day < 1 || day > 31 {
				lastTok = tokOther
				continue
			}
			addAtom(day, 0, false)
			lastTok = tokAtom
		case m[6] != "":
			year, _ := strconv.Atoi(m[6])
			if (lastTok == tokMonth || lastTok == tokAtom) && year >= 2000 && year <= 2100 {
				propagate(0, year)
				lastTok = tokYear
			} else {
				lastTok = tokOther
			}
		case m[7] != "":
			word := m[7]
			lastTok = tokOther
			if monthStr, ok := croatianMonths[word]; ok {
				if len(atoms) > 0 && atoms[len(atoms)-1].month == 0 {
					month, _ := strconv.Atoi(monthStr)
					propagate(month, 0)
					lastTok = tokMonth
				}
				continue
			}
			switch word {
			case "od":
				closeExclusion()
				pendingOd = true
			case "do":
				if openSpan >= 0 && !spans[openSpan].closed {
					pendingEnd = openSpan
				} else {
					pendingUntil = true
				}
				openSpan = -1
			case "osim":
				closeExclusion()
				excl = &dateExclusion{fromSpan: scopeStart, weekdays: map[time.Weekday]bool{}}
			default:
				if excl != nil {
					for _, wd := range croatianWeekdays[word] {
						excl.weekdays[wd] = true
					}
				}
			}
		case m[8] != "":
			// "19. - 30.10."
			if lastTok != tokOther && len(spans) > 0 && !spans[len(spans)-1].closed && excl == nil {
				pendingEnd = len(spans) - 1
			}
			lastTok = tokOther
		case m[9] != "":
			// "2026.," ends the date, not the sentence
			if m[9] == "." && lastTok != tokYear {
				closeExclusion()
			}
			if lastTok != tokYear {
				lastTok = tokOther
			}
		}
	}
	closeExclusion()

	// Days without a stated year take the year of the next dated day, else this year.
	nextYear := nowYear
	for i := len(atoms) - 1; i >= 0; i-- {
		if atoms[i].year == 0 {
			atoms[i].year = nextYear
		} else {
			nextYear = atoms[i].year
		}
	}

	toDate := func(a dateAtom) (time.Time, bool) {
		if a.month == 0 {
			return time.Time{}, false
		}
		t := time.Date(a.year, time.Month(a.month), a.day, 0, 0, 0, 0, time.UTC)
		return t, t.Day() == a.day
	}

	dateSet := make(map[string]bool)
	spanOf := make(map[string][]int)
	var openEnd string
	for si, sp := range spans {
		end, ok := toDate(atoms[sp.end])
		if !ok {
			continue
		}
		if sp.start < 0 {
			if d := end.Format("2006-01-02"); d > openEnd {
				openEnd = d
			}
			continue
		}
		if a := atoms[sp.start]; sp.start == sp.end && !isTitle && a.numeric && !a.explicitYear {
			continue
		}
		start, ok := toDate(atoms[sp.start])
		if !ok {
			continue
		}
		if start.After(end) {
			// "od 28. prosinca do 3. siječnja 2027." - the start year was inferred
			start = start.AddDate(-1, 0, 0)
		}
		for d, n := start, 0; !d.After(end) && n < 400; d, n = d.AddDate(0, 0, 1), n+1 {
			key := d.Format("2006-01-02")
			dateSet[key] = true
			spanOf[key] = append(spanOf[key], si)
		}
	}

	for _, ex := range exclusions {
		for _, ai := range ex.atoms {
			if t, ok := toDate(atoms[ai]); ok {
				delete(dateSet, t.Format("2006-01-02"))
			}
		}
		if len(ex.weekdays) == 0 {
			continue
		}
		for key := range dateSet {
			t, _ := time.Parse("2006-01-02", key)
			if !ex.weekdays[t.Weekday()] {
				continue
			}
			for _, si := range spanOf[key] {
				if si >= ex.fromSpan && si < ex.toSpan {
					delete(dateSet, key)
					break
				}
			}
		}
	}

	if openEnd != "" {
		// Open-ended notice: active from now until the last day.
		for d := range dateSet {
			if d > openEnd {
				openEnd = d
			}
		}
		return nil, "", openEnd
	}

	var dates []string
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return nil, "", ""
	}
	return dates, dates[0], dates[len(dates)-1]
}

func extractPDFURLs(html string) []string {
	var urls []string
	matches := pdfURLRegex.FindAllString(html, -1)
	for _, m := range matches {
		if !strings.HasPrefix(m, "http") {
			m = "https://www.hzpp.hr" + m
		}
		urls = append(urls, m)
	}

	hrefMatches := hrefPDFRegex.FindAllStringSubmatch(html, -1)
	for _, hm := range hrefMatches {
		if len(hm) > 1 {
			u := hm[1]
			if !strings.HasPrefix(u, "http") {
				if strings.HasPrefix(u, "/") {
					u = "https://www.hzpp.hr" + u
				} else {
					u = "https://www.hzpp.hr/" + u
				}
			}
			urls = append(urls, u)
		}
	}
	return uniqueStrings(urls)
}

func extractTrainNumbers(text string) []string {
	rawMatches := trainNumRegex.FindAllString(text, -1)
	var filtered []string
	for _, m := range rawMatches {
		if m == "2024" || m == "2025" || m == "2026" || m == "2027" || strings.HasSuffix(m, "000") {
			continue
		}
		filtered = append(filtered, m)
	}
	return uniqueStrings(filtered)
}

// linePrefixRegex matches a railway line designation ("na pruzi Zagreb GK – Dugo Selo",
// "pruga M604 Dugo Selo - Botovo") up to the first keyword that follows it. The keyword is
// captured so it can be kept.
var linePrefixRegex = regexp.MustCompile(`(?i)(?:na\s+pruzi|pruga|pruzi|pruge)\s+[A-ČĆŽŠĐa-čćžšđ0-9\s–.-]+?(između|bit\s+će|zbog|radi|zatvorena|otvorena|vozit|u\s+razdoblju|od\s+\d)`)

func stripLineNamePrefixes(text string) string {
	// Remove expressions like "na pruzi Zagreb GK – Dugo Selo između" while retaining the trailing keyword "između"
	return linePrefixRegex.ReplaceAllString(text, " ${1}")
}

func extractStations(text string, allStations []string) []string {
	// Strip railway line name designations (e.g., "na pruzi Zagreb GK – Dugo Selo", "pruga M604 Dugo Selo - Botovo")
	// so line endpoints are not falsely parsed as affected stations unless explicitly named in affected sections.
	var found []string
	for _, m := range matchStations(stripLineNamePrefixes(text), allStations) {
		found = append(found, m.name)
	}
	return uniqueStrings(found)
}

type stationMatch struct {
	name string
	pos  int // byte offset of the first occurrence
}

// matchStations finds the stations named in text as whole words, in order of first
// occurrence.
func matchStations(text string, allStations []string) []stationMatch {
	// Hyphens and spaces are interchangeable: GTFS "Hum-Lug" is written "Hum Lug"
	normalize := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "-", " ") }
	lowerText := normalize(text)

	// Longest names first: each match is blanked out so "Split" is not found again
	// inside "Split Predgrađe", nor "Koprivnica" inside "Bučje-Koprivnica".
	sorted := make([]string, 0, len(allStations))
	for _, st := range allStations {
		if st = strings.TrimSpace(st); utf8.RuneCountInString(st) >= 3 {
			sorted = append(sorted, st)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return utf8.RuneCountInString(sorted[i]) > utf8.RuneCountInString(sorted[j])
	})

	var found []stationMatch
	seen := make(map[string]bool)
	for _, st := range sorted {
		stLower := normalize(st)
		first := -1
		for from := 0; from < len(lowerText); {
			idx := strings.Index(lowerText[from:], stLower)
			if idx < 0 {
				break
			}
			start, end := from+idx, from+idx+len(stLower)
			from = end
			// Whole words only: "kolosijekom" must not match "Osijek"
			if before, _ := utf8.DecodeLastRuneInString(lowerText[:start]); start > 0 && isWordRune(before) {
				continue
			}
			if after, _ := utf8.DecodeRuneInString(lowerText[end:]); end < len(lowerText) && isWordRune(after) {
				continue
			}
			if first < 0 {
				first = start
			}
			lowerText = lowerText[:start] + strings.Repeat(" ", end-start) + lowerText[end:]
		}
		if first >= 0 && !seen[st] {
			seen[st] = true
			found = append(found, stationMatch{name: st, pos: first})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].pos < found[j].pos })
	return found
}

var (
	// "između kolodvora Duga Resa i Oštarije od 19. ...", "između kolodvora Buzet, Lupoglav i Pazin od ..."
	sectionBetweenRegex = regexp.MustCompile(`(?i)između\s+(?:kolodvora|stajališta)\s+([^\d.]+?)(?:\s+(?:od|do|promet|bit|putnike|zbog|u)\s|\s*\d|\.|$)`)
	// "pruga Varaždin – Koprivnica bit će zatvorena za promet od 9. ..." (no "između")
	sectionLineRegex = regexp.MustCompile(`(?i)pruga\s+([^\d.]+?)\s+bit\s+će\s+zatvorena\s+za\s+promet\s+(?:od|do)\s`)
)

// extractSections returns the closed track sections a notice names, each as the ordered
// stations along it, e.g. [["Duga Resa", "Oštarije"]]. Sections whose ends are not both
// known stations (e.g. "Križevci i Gyekenyes") are left out.
func extractSections(text string, allStations []string) [][]string {
	var sections [][]string
	collect := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			var section []string
			for _, st := range matchStations(m[1], allStations) {
				section = append(section, st.name)
			}
			if len(section) >= 2 {
				sections = append(sections, section)
			}
		}
	}
	collect(sectionBetweenRegex)
	if len(sections) == 0 {
		collect(sectionLineRegex)
	}
	return sections
}

// "pruga Banova Jaruga – Pčelić bit će zatvorena", "na pruzi Novska – Tovarnik – državna granica između"
var lineNameRegex = regexp.MustCompile(`(?i)(?:na\s+pruzi|pruga)\s+([^\d.]+?)\s+(?:bit\s+će|između|zbog|od\s+\d)`)

// extractLine returns the known stations of the railway line a notice names, in order.
// Notices sometimes give a section that runs past the end of the closed line ("pruga
// Banova Jaruga – Pčelić ... između kolodvora Banova Jaruga, Daruvar i Virovitica",
// where Pčelić – Virovitica belongs to another, open line), so matching clips sections
// to the line.
func extractLine(text string, allStations []string) []string {
	m := lineNameRegex.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var line []string
	for _, st := range matchStations(m[1], allStations) {
		line = append(line, st.name)
	}
	if len(line) < 2 {
		return nil
	}
	return line
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func extractEmbeddedJSONData(sec string) ([]string, string, string) {
	startIdx := strings.Index(sec, "{{")
	endIdx := strings.Index(sec, "}}")

	if startIdx != -1 && endIdx > startIdx {
		// "{{ ... }}" wraps the object body in doubled braces; keep one pair so it is a JSON object.
		jsonStr := sec[startIdx+1 : endIdx+1]
		jsonStr = stripHTMLTags(jsonStr)
		jsonStr = strings.ReplaceAll(jsonStr, "&nbsp;", " ")
		jsonStr = strings.TrimSpace(jsonStr)

		reTrailingComma := regexp.MustCompile(`,(\s*[\]}])`)
		jsonStr = reTrailingComma.ReplaceAllString(jsonStr, "$1")

		var embed EmbeddedJSONNotice
		if err := json.Unmarshal([]byte(jsonStr), &embed); err == nil {
			var dateFrom, dateTo string
			if len(embed.Dates) > 0 {
				dateFrom = embed.Dates[0].From
				dateTo = embed.Dates[0].To
			}
			return embed.StationsHR, dateFrom, dateTo
		}
	}
	return nil, "", ""
}

func saveNoticesToDB(database *sql.DB, notices []NoticeItem) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM traffic_notices"); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO traffic_notices (
			id, category, title, content, parsed_stations, parsed_trains, pdf_urls,
			is_bus_replacement, is_track_works, date_from, date_to, dates_json, updated_at,
			section_stations, line_stations
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range notices {
		stationsJSON, _ := json.Marshal(n.ParsedStations)
		trainsJSON, _ := json.Marshal(n.ParsedTrains)
		pdfsJSON, _ := json.Marshal(n.PDFURLs)
		datesJSON, _ := json.Marshal(n.DatesJSON)
		sectionsJSON, _ := json.Marshal(n.SectionStations)
		lineJSON, _ := json.Marshal(n.LineStations)

		isBusInt := 0
		if n.IsBusReplacement {
			isBusInt = 1
		}
		isWorksInt := 0
		if n.IsTrackWorks {
			isWorksInt = 1
		}

		_, err := stmt.Exec(
			n.ID, n.Category, n.Title, n.Content,
			string(stationsJSON), string(trainsJSON), string(pdfsJSON),
			isBusInt, isWorksInt, n.DateFrom, n.DateTo, string(datesJSON), n.UpdatedAt,
			string(sectionsJSON), string(lineJSON),
		)
		if err != nil {
			log.Printf("Warning: error inserting notice %s: %v", n.ID, err)
		}
	}

	return tx.Commit()
}

func StartNoticesBackgroundWorker(database *sql.DB, getStationsFunc func() []string) {
	go func() {
		stations := getStationsFunc()
		if err := FetchAndProcessNotices(database, stations); err != nil {
			log.Printf("Startup notice fetch warning: %v", err)
		}

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			now := time.Now().In(time.Local)
			if now.Hour() >= 6 && now.Hour() <= 22 {
				log.Println("Periodic daytime notice check running...")
				stList := getStationsFunc()
				if err := FetchAndProcessNotices(database, stList); err != nil {
					log.Printf("Periodic notice fetch error: %v", err)
				}
			}
		}
	}()
}

func stripHTMLTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, " ")
}

func mergeUnique(a, b []string) []string {
	m := make(map[string]bool)
	for _, item := range a {
		m[item] = true
	}
	for _, item := range b {
		m[item] = true
	}
	var res []string
	for item := range m {
		res = append(res, item)
	}
	sort.Strings(res)
	return res
}

func uniqueStrings(slice []string) []string {
	return mergeUnique(slice, nil)
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}
