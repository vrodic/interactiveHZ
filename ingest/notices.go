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
)

type NoticeItem struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"` // "info" or "works"
	Title            string   `json:"title"`
	Content          string   `json:"content"`
	ParsedStations   []string `json:"parsed_stations"`
	ParsedTrains     []string `json:"parsed_trains"`
	PDFURLs          []string `json:"pdf_urls"`
	IsBusReplacement bool     `json:"is_bus_replacement"`
	IsTrackWorks     bool     `json:"is_track_works"`
	DateFrom         string   `json:"date_from,omitempty"`
	DateTo           string   `json:"date_to,omitempty"`
	DatesJSON        []string `json:"dates_json,omitempty"`
	UpdatedAt        string   `json:"updated_at"`
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
	lastInfoHash   string
	lastWorksHash  string
)

var (
	trainNumRegex  = regexp.MustCompile(`\b[1-9]\d{2,4}\b`)
	pdfURLRegex    = regexp.MustCompile(`https?://www\.hzpp\.hr/api/v1/repository/download/[a-f0-9\-]+`)
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
		"studeni": "11", "studenoga": "11", "studenom": "11",
		"prosinac": "12", "prosinca": "12", "prosinci": "12",
	}
)

func FetchAndProcessNotices(database *sql.DB, allStations []string) error {
	validTrainNums := fetchValidTrainNumbers(database)

	infoChanged, infoItems, err := scrapePage("info", "https://www.hzpp.hr/hr/informacije?type=info", database, allStations, validTrainNums)
	if err != nil {
		log.Printf("Error scraping info page: %v", err)
	}

	worksChanged, worksItems, err := scrapePage("works", "https://www.hzpp.hr/hr/informacije?type=works", database, allStations, validTrainNums)
	if err != nil {
		log.Printf("Error scraping works page: %v", err)
	}

	if !infoChanged && !worksChanged {
		log.Println("Traffic notices and track works pages unchanged.")
		return nil
	}

	allNotices := append(infoItems, worksItems...)
	if err := saveNoticesToDB(database, allNotices); err != nil {
		return fmt.Errorf("failed to save notices to DB: %w", err)
	}

	log.Printf("Successfully ingested %d traffic notices and track works.", len(allNotices))
	return nil
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

func scrapePage(category string, pageURL string, database *sql.DB, allStations []string, validTrainNums map[string]bool) (bool, []NoticeItem, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, nil, err
	}

	hash := sha256.Sum256(bodyBytes)
	hashStr := hex.EncodeToString(hash[:])

	noticeHashesMu.Lock()
	if category == "info" {
		if hashStr == lastInfoHash {
			noticeHashesMu.Unlock()
			return false, nil, nil
		}
		lastInfoHash = hashStr
	} else {
		if hashStr == lastWorksHash {
			noticeHashesMu.Unlock()
			return false, nil, nil
		}
		lastWorksHash = hashStr
	}
	noticeHashesMu.Unlock()

	htmlText := string(bodyBytes)
	items := parseNoticesFromHTML(category, htmlText, allStations, validTrainNums, client)

	return true, items, nil
}

func parseNoticesFromHTML(category string, html string, allStations []string, validTrainNums map[string]bool, client *http.Client) []NoticeItem {
	var items []NoticeItem

	sections := buttonTagRegex.Split(html, -1)
	if len(sections) <= 1 {
		sections = []string{html}
	}

	for idx, sec := range sections {
		cleanText := stripHTMLTags(sec)
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
			strings.Contains(titleLower, "povrat novca") {
			continue
		}

		content := strings.Join(contentLines, "\n")
		fullSectionText := sec + "\n" + cleanText

		pdfURLs := extractPDFURLs(fullSectionText)
		rawTrains := extractTrainNumbers(fullSectionText)
		stations := extractStations(fullSectionText, allStations)

		// Check embedded JSON
		embeddedStations, dateFrom, dateTo := extractEmbeddedJSONData(sec)
		if len(embeddedStations) > 0 {
			stations = mergeUnique(stations, embeddedStations)
		}

		// Download and parse PDFs if present
		if client != nil {
			for _, pdfURL := range pdfURLs {
				pdfTrains := extractTrainsFromPDF(client, pdfURL)
				if len(pdfTrains) > 0 {
					rawTrains = mergeUnique(rawTrains, pdfTrains)
				}
			}
		}

		// Filter trains against valid database GTFS train numbers
		var validTrains []string
		for _, tr := range rawTrains {
			if validTrainNums[tr] {
				validTrains = append(validTrains, tr)
			}
		}

		// Parse dates from text
		datesList, dfText, dtText := parseDatesFromText(cleanText)
		if dateFrom == "" {
			dateFrom = dfText
		}
		if dateTo == "" {
			dateTo = dtText
		}

		// Combine embedded dates with text dates
		if dateFrom != "" && !containsString(datesList, dateFrom) {
			datesList = append(datesList, dateFrom)
		}
		if dateTo != "" && !containsString(datesList, dateTo) {
			datesList = append(datesList, dateTo)
		}
		sort.Strings(datesList)

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

func parseDatesFromText(text string) ([]string, string, string) {
	var dates []string
	var df, dt string

	lower := strings.ToLower(text)
	nowYear := time.Now().Year()

	// Pattern 1: od DD. [month] do DD. [month] YYYY.
	// Pattern 2: DD., DD. i DD. [month] YYYY.
	// Pattern 3: DD.MM.YYYY. - DD.MM.YYYY.
	// Pattern 4: DD.MM.YYYY.

	// Extract explicit YYYY-MM-DD pattern: DD.MM.YYYY.
	reIsoDate := regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\.(\d{4})\.?`)
	matchesIso := reIsoDate.FindAllStringSubmatch(text, -1)
	for _, m := range matchesIso {
		day, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		formatted := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
		dates = append(dates, formatted)
	}

	// Pattern DD. - DD.MM. e.g. 7. - 8.11. or 19. - 23.10.
	reRangeShort := regexp.MustCompile(`\b(\d{1,2})\.\s*-\s*(\d{1,2})\.(\d{1,2})\.?`)
	matchesRangeShort := reRangeShort.FindAllStringSubmatch(text, -1)
	for _, m := range matchesRangeShort {
		dStart, _ := strconv.Atoi(m[1])
		dEnd, _ := strconv.Atoi(m[2])
		month, _ := strconv.Atoi(m[3])
		if month >= 1 && month <= 12 && dStart <= dEnd && dEnd <= 31 {
			for d := dStart; d <= dEnd; d++ {
				formatted := fmt.Sprintf("%04d-%02d-%02d", nowYear, month, d)
				dates = append(dates, formatted)
			}
		}
	}

	// Pattern DD.MM. - DD.MM. e.g. 7.11. - 8.11.
	reRangeFullMonth := regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\.\s*-\s*(\d{1,2})\.(\d{1,2})\.?`)
	matchesRangeFullMonth := reRangeFullMonth.FindAllStringSubmatch(text, -1)
	for _, m := range matchesRangeFullMonth {
		dStart, _ := strconv.Atoi(m[1])
		mStart, _ := strconv.Atoi(m[2])
		dEnd, _ := strconv.Atoi(m[3])
		mEnd, _ := strconv.Atoi(m[4])
		if mStart == mEnd && dStart <= dEnd && mStart >= 1 && mStart <= 12 {
			for d := dStart; d <= dEnd; d++ {
				formatted := fmt.Sprintf("%04d-%02d-%02d", nowYear, mStart, d)
				dates = append(dates, formatted)
			}
		} else if mStart >= 1 && mStart <= 12 && mEnd >= 1 && mEnd <= 12 {
			dates = append(dates, fmt.Sprintf("%04d-%02d-%02d", nowYear, mStart, dStart))
			dates = append(dates, fmt.Sprintf("%04d-%02d-%02d", nowYear, mEnd, dEnd))
		}
	}

	// Pattern single DD.MM. e.g. 18.10. (MUST have trailing dot to distinguish from time e.g. 4.05)
	reSingleShort := regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\.`)
	matchesSingleShort := reSingleShort.FindAllStringSubmatch(text, -1)
	for _, m := range matchesSingleShort {
		day, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			formatted := fmt.Sprintf("%04d-%02d-%02d", nowYear, month, day)
			dates = append(dates, formatted)
		}
	}

	// Extract Croatian text dates with month names e.g. "22., 23., 26. i 27. listopada 2026."
	reMonthText := regexp.MustCompile(`((?:\d{1,2}\.\s*(?:,\s*|i\s*|do\s*)?)+)\s*([a-čćžšđA-ČĆŽŠĐ]+)\s*(\d{4})?\.?`)
	matchesMonth := reMonthText.FindAllStringSubmatch(lower, -1)
	for _, m := range matchesMonth {
		daysRaw := m[1]
		monthName := m[2]
		yearStr := m[3]

		monthNum, ok := croatianMonths[monthName]
		if !ok {
			continue
		}

		year := nowYear
		if yearStr != "" {
			if y, err := strconv.Atoi(yearStr); err == nil {
				year = y
			}
		}

		reDayNum := regexp.MustCompile(`\b\d{1,2}\b`)
		dayNums := reDayNum.FindAllString(daysRaw, -1)
		for _, dStr := range dayNums {
			day, _ := strconv.Atoi(dStr)
			formatted := fmt.Sprintf("%04d-%s-%02d", year, monthNum, day)
			dates = append(dates, formatted)
		}
	}

	dates = uniqueStrings(dates)
	sort.Strings(dates)

	if len(dates) > 0 {
		df = dates[0]
		dt = dates[len(dates)-1]
	}

	return dates, df, dt
}

func extractPDFURLs(html string) []string {
	matches := pdfURLRegex.FindAllString(html, -1)
	return uniqueStrings(matches)
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

func extractStations(text string, allStations []string) []string {
	var found []string
	lowerText := strings.ToLower(text)

	for _, st := range allStations {
		stClean := strings.TrimSpace(st)
		if len(stClean) < 3 {
			continue
		}
		stLower := strings.ToLower(stClean)

		if strings.Contains(lowerText, stLower) {
			found = append(found, stClean)
		}
	}
	return uniqueStrings(found)
}

func extractEmbeddedJSONData(sec string) ([]string, string, string) {
	startIdx := strings.Index(sec, "{{")
	endIdx := strings.Index(sec, "}}")

	if startIdx != -1 && endIdx > startIdx {
		jsonStr := sec[startIdx+2 : endIdx]
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

func extractTrainsFromPDF(client *http.Client, pdfURL string) []string {
	req, err := http.NewRequest("GET", pdfURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	text := string(data)
	return extractTrainNumbers(text)
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
			is_bus_replacement, is_track_works, date_from, date_to, dates_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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

func containsString(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}
