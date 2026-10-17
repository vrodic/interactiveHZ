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
	trainNumRegex  = regexp.MustCompile(`\b[1-9]\d{3,4}\b`)
	pdfURLRegex    = regexp.MustCompile(`https?://www\.hzpp\.hr/api/v1/repository/download/[a-f0-9\-]+`)
	buttonTagRegex = regexp.MustCompile(`(?i)<button[^>]*>`)
)

func FetchAndProcessNotices(database *sql.DB, allStations []string) error {
	infoChanged, infoItems, err := scrapePage("info", "https://www.hzpp.hr/hr/informacije?type=info", database, allStations)
	if err != nil {
		log.Printf("Error scraping info page: %v", err)
	}

	worksChanged, worksItems, err := scrapePage("works", "https://www.hzpp.hr/hr/informacije?type=works", database, allStations)
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

func scrapePage(category string, pageURL string, database *sql.DB, allStations []string) (bool, []NoticeItem, error) {
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
	items := parseNoticesFromHTML(category, htmlText, allStations, client)

	return true, items, nil
}

func parseNoticesFromHTML(category string, html string, allStations []string, client *http.Client) []NoticeItem {
	var items []NoticeItem

	// Split by accordion button or main sections
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

		// Check if section looks like a notice header or notice content
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

		content := strings.Join(contentLines, "\n")
		fullSectionText := sec + "\n" + cleanText

		pdfURLs := extractPDFURLs(fullSectionText)
		trains := extractTrainNumbers(fullSectionText)
		stations := extractStations(fullSectionText, allStations)

		// Check for embedded JSON in section
		embeddedStations, dateFrom, dateTo := extractEmbeddedJSONData(sec)
		if len(embeddedStations) > 0 {
			stations = mergeUnique(stations, embeddedStations)
		}

		// Download and parse PDFs if present to enrich train numbers
		if client != nil {
			for _, pdfURL := range pdfURLs {
				pdfTrains := extractTrainsFromPDF(client, pdfURL)
				if len(pdfTrains) > 0 {
					trains = mergeUnique(trains, pdfTrains)
				}
			}
		}

		isBus := strings.Contains(strings.ToLower(fullSectionText), "autobus") ||
			strings.Contains(strings.ToLower(fullSectionText), "zamjensk") ||
			strings.Contains(strings.ToLower(fullSectionText), "minibus")

		isTrackWorks := category == "works" ||
			strings.Contains(strings.ToLower(fullSectionText), "radov") ||
			strings.Contains(strings.ToLower(fullSectionText), "zatvoren") ||
			strings.Contains(strings.ToLower(fullSectionText), "održavanj")

		// Filter out noise sections
		if len(stations) == 0 && len(trains) == 0 && !isBus && !isTrackWorks {
			continue
		}

		noticeID := fmt.Sprintf("%s-%d-%s", category, idx, hashString(title))

		items = append(items, NoticeItem{
			ID:               noticeID,
			Category:         category,
			Title:            title,
			Content:          content,
			ParsedStations:   stations,
			ParsedTrains:     trains,
			PDFURLs:          pdfURLs,
			IsBusReplacement: isBus,
			IsTrackWorks:     isTrackWorks,
			DateFrom:         dateFrom,
			DateTo:           dateTo,
			UpdatedAt:        time.Now().In(time.Local).Format("2006-01-02 15:04:05"),
		})
	}

	return items
}

func extractPDFURLs(html string) []string {
	matches := pdfURLRegex.FindAllString(html, -1)
	return uniqueStrings(matches)
}

func extractTrainNumbers(text string) []string {
	rawMatches := trainNumRegex.FindAllString(text, -1)
	var filtered []string
	for _, m := range rawMatches {
		// Avoid matching zip codes or years like 2026, 10000, 41000
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

		// Word boundary check in lower text
		if strings.Contains(lowerText, stLower) {
			found = append(found, stClean)
		}
	}
	return uniqueStrings(found)
}

func extractEmbeddedJSONData(sec string) ([]string, string, string) {
	// Look for {{ ... }} in html
	startIdx := strings.Index(sec, "{{")
	endIdx := strings.Index(sec, "}}")

	if startIdx != -1 && endIdx > startIdx {
		jsonStr := sec[startIdx+2 : endIdx]
		// Clean html tags like <br />, &nbsp; inside json snippet
		jsonStr = stripHTMLTags(jsonStr)
		jsonStr = strings.ReplaceAll(jsonStr, "&nbsp;", " ")
		jsonStr = strings.TrimSpace(jsonStr)

		// Fix trailing commas in JSON array/object before closing brackets
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

	// PDF binary parsing for train numbers in text streams
	text := string(data)
	return extractTrainNumbers(text)
}

func saveNoticesToDB(database *sql.DB, notices []NoticeItem) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear previous notices to sync
	if _, err := tx.Exec("DELETE FROM traffic_notices"); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO traffic_notices (
			id, category, title, content, parsed_stations, parsed_trains, pdf_urls,
			is_bus_replacement, is_track_works, date_from, date_to, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range notices {
		stationsJSON, _ := json.Marshal(n.ParsedStations)
		trainsJSON, _ := json.Marshal(n.ParsedTrains)
		pdfsJSON, _ := json.Marshal(n.PDFURLs)

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
			isBusInt, isWorksInt, n.DateFrom, n.DateTo, n.UpdatedAt,
		)
		if err != nil {
			log.Printf("Warning: error inserting notice %s: %v", n.ID, err)
		}
	}

	return tx.Commit()
}

func StartNoticesBackgroundWorker(database *sql.DB, getStationsFunc func() []string) {
	go func() {
		// Initial fetch on startup
		stations := getStationsFunc()
		if err := FetchAndProcessNotices(database, stations); err != nil {
			log.Printf("Startup notice fetch warning: %v", err)
		}

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			now := time.Now().In(time.Local)
			// Daytime check between 06:00 and 22:00
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
