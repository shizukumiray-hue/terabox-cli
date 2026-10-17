package terabox

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	baseURLFlow    = "https://flowvideoplayer.com"
	searchEndpoint = "/telegram/bot/search/video"
)

// Request body
type searchRequest struct {
	URL string `json:"url"`
}

// Raw response dari API
type rawFileData struct {
	FileName       string `json:"file_name"`
	Thumbnail      string `json:"thumbnail"`
	DownloadURL    string `json:"download_url"`
	StreamFinalURL string `json:"stream_final_url"`
	FileSize       string `json:"file_size"`
	FileSizeBytes  int64  `json:"file_size_bytes"`
}

type searchResponse struct {
	Error bool          `json:"error"`
	Data  []rawFileData `json:"data"`
}

// Result struct sesuai permintaan
type Result struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	FileSize  string `json:"file_size"`
	CoverURL  string `json:"cover_url"`
	StreamURL string `json:"stream_url"`
	FileURL   string `json:"file_url"`
}

// generateID dari short URL (bagian setelah /s/) menjadi 10 digit angka
func GenerateID(shortURL string) (string, error) {
	// Parse URL untuk mengambil path
	parsed, err := url.Parse(shortURL)
	if err != nil {
		return "", err
	}
	path := parsed.Path
	// Cari segmen setelah "/s/"
	parts := strings.Split(path, "/s/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid terabox URL format, missing /s/")
	}
	code := parts[1]
	// Hilangkan trailing slash atau query jika ada
	code = strings.Split(code, "?")[0]
	code = strings.Split(code, "/")[0]

	// Buat hash MD5, ambil 10 digit desimal dari hash
	hash := md5.Sum([]byte(code))
	hexHash := hex.EncodeToString(hash[:])
	// Ambil 10 karakter hex pertama, konversi ke angka desimal, lalu pad ke 10 digit
	// (Lebih stabil daripada modulo karena panjang tetap 10 digit)
	hexPart := hexHash[:8] // 8 hex = 32 bit
	val, err := strconv.ParseUint(hexPart, 16, 64)
	if err != nil {
		return "", err
	}
	// Modulo 10^10 agar muat 10 digit
	idNum := val % 10000000000
	idStr := fmt.Sprintf("%010d", idNum)
	return idStr, nil
}

// extractCSRFToken dari HTML
func extractCSRFToken(html string) string {
	re := regexp.MustCompile(`<meta\s+name=["']csrf-token["']\s+content=["']([^"']+)["']`)
	matches := re.FindStringSubmatch(html)
	if len(matches) >= 2 {
		return matches[1]
	}
	re2 := regexp.MustCompile(`<meta\s+content=["']([^"']+)["']\s+name=["']csrf-token["']`)
	matches2 := re2.FindStringSubmatch(html)
	if len(matches2) >= 2 {
		return matches2[1]
	}
	return ""
}

// FetchMedia menerima URL terabox dan mengembalikan slice Result beserta error
func FetchMedia(teraboxURL string) ([]Result, error) {
	// 1. HTTP client dengan cookie jar
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 40 * time.Second,
	}

	// 2. GET halaman utama untuk dapatkan cookie dan CSRF token
	homeReq, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return nil, err
	}
	homeReq.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	homeReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9")
	homeReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(homeReq)
	if err != nil {
		return nil, fmt.Errorf("homepage request failed: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(bodyBytes)

	csrfToken := extractCSRFToken(html)
	if csrfToken == "" {
		return nil, fmt.Errorf("failed to extract CSRF token")
	}

	// 3. POST request ke endpoint search
	reqBody := searchRequest{URL: teraboxURL}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURLFlow+searchEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-TOKEN", csrfToken)
	req.Header.Set("Referer", baseURLFlow+"/")
	req.Header.Set("Origin", baseURLFlow)
	req.Header.Set("User-Agent", homeReq.Header.Get("User-Agent"))

	resp2, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp2.Body.Close()

	respBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil, err
	}

	var apiResp searchResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	if apiResp.Error {
		return nil, fmt.Errorf("API returned error")
	}

	// 4. Generate ID dari short URL
	id, err := GenerateID(teraboxURL)
	if err != nil {
		return nil, err
	}

	// 5. Konversi ke Result
	var results []Result
	for _, raw := range apiResp.Data {
		res := Result{
			ID:        id, // ID sama untuk semua file dari satu URL (bisa disesuaikan jika perlu per file)
			FileName:  raw.FileName,
			FileSize:  raw.FileSize,
			CoverURL:  raw.Thumbnail,
			StreamURL: raw.StreamFinalURL,
			FileURL:   raw.DownloadURL,
		}
		// Jika ada field kosong, tetap aman karena default string kosong
		results = append(results, res)
	}

	return results, nil
}
