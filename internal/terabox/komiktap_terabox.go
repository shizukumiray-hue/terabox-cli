package terabox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type komiktapResponse struct {
	Status          string              `json:"status"`
	Source          string              `json:"source"`
	RequestURL      string              `json:"request_url"`
	ExtractShorturl string              `json:"extract_shorturl"`
	TotalFiles      int                 `json:"total_files"`
	Files           []komiktapFileEntry `json:"files"`
}

type komiktapFileEntry struct {
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	Path          string `json:"path"`
	BokepDetected int    `json:"bokep_detected"`
	Dlink         string `json:"dlink"`
	Thumbnail     string `json:"thumbnail"`
}

func FetchKomiktapUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	apiEndpoint := "https://komiktap.eu.cc/terabox?url=" + url.QueryEscape(teraboxURL)

	client := &http.Client{Timeout: 40 * time.Second}
	req, err := http.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("komiktap request creation error: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("komiktap request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("komiktap returned status HTTP %d", resp.StatusCode)
	}

	var result komiktapResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("komiktap json decode error: %w", err)
	}

	if result.Status != "success" && len(result.Files) == 0 {
		return nil, fmt.Errorf("komiktap response status: %s", result.Status)
	}

	id := result.ExtractShorturl
	if id == "" {
		id = "komiktap"
	}

	var output []TeraboxUniversalData
	for _, f := range result.Files {
		output = append(output, TeraboxUniversalData{
			ID:          id,
			FileName:    f.Filename,
			FileSize:    teraboxFormatFileSize(f.Size),
			Thumbnail:   f.Thumbnail,
			StreamURL:   f.Dlink,
			DownloadURL: f.Dlink,
		})
	}

	return output, nil
}
