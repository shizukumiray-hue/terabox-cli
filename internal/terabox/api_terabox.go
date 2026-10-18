package terabox

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// teraboxFile untuk parsing respons Netlify
type teraboxFile struct {
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	DirectLink string `json:"direct_link"`
}

type teraboxResponse struct {
	Total int           `json:"total"`
	Files []teraboxFile `json:"files"`
}

// fetchTeraboxData melakukan request ke API Netlify
func fetchTeraboxData(apiURL string) (*teraboxResponse, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var data teraboxResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("unmarshal error (%s): %w", string(body), err)
	}

	if len(data.Files) == 0 {
		return nil, fmt.Errorf("tidak ada file di dalam response")
	}
	return &data, nil
}

func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// FetchTeraboxAPI2 menggunakan API cadangan Netlify dan mengembalikan []TeraboxUniversalData
func FetchTeraboxAPI2(teraboxURL string) ([]TeraboxUniversalData, error) {
	baseURL := "https://steady-mousse-3e1cb5.netlify.app/api/terabox?url="
	apiURL := baseURL + url.QueryEscape(teraboxURL)
	resp, err := fetchTeraboxData(apiURL)
	if err != nil {
		return nil, err
	}

	// Generate ID dari URL terabox (fungsi exported dari package yang sama)
	id, err := GenerateID(teraboxURL)
	if err != nil {
		return nil, err
	}

	var result []TeraboxUniversalData
	for _, f := range resp.Files {
		result = append(result, TeraboxUniversalData{
			ID:          id,
			FileName:    f.Filename,
			FileSize:    FormatBytes(f.Size), // konversi byte ke human readable (KB/MB/GB)
			Thumbnail:   "",                  // API tidak menyediakan thumbnail
			StreamURL:   "",                  // API tidak menyediakan stream URL
			DownloadURL: f.DirectLink,
		})
	}
	return result, nil
}

// FetchTeraboxWithFallback mencoba API cadangan dan mengembalikan []TeraboxUniversalData
func FetchTeraboxWithFallback(teraboxURL string) ([]TeraboxUniversalData, error) {
	slog.Info("Mencoba API Terabox", "api", "Netlify")
	data, err := FetchTeraboxAPI2(teraboxURL)
	if err == nil && len(data) > 0 {
		slog.Info("API Terabox berhasil!", "api", "Netlify", "total_files", len(data))
		return data, nil
	}
	slog.Warn("API Terabox gagal", "api", "Netlify", "error", err)
	return nil, fmt.Errorf("semua API Terabox gagal: %v", err)
}
