package terabox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type sechnoRequest struct {
	URL string `json:"url"`
}

type sechnoFile struct {
	Filename      string      `json:"filename"`
	Size          interface{} `json:"size"`
	SizeFormatted string      `json:"sizeFormatted"`
	IsDir         bool        `json:"isDir"`
	DownloadURL   string      `json:"downloadUrl"`
	StreamURL     string      `json:"streamUrl"`
	ThumbURL      string      `json:"thumbUrl"`
}

type sechnoResponse struct {
	Success bool         `json:"success"`
	Error   string       `json:"error"`
	Title   string       `json:"title"`
	Files   []sechnoFile `json:"files"`
}

var sechnoURLRegex = regexp.MustCompile(`https?://[^\s]+`)

func extractSechnoCleanURL(text string) string {
	if text == "" {
		return ""
	}
	clean := sechnoURLRegex.FindString(text)
	if clean == "" {
		clean = strings.TrimSpace(text)
	}
	if !strings.HasPrefix(clean, "http://") && !strings.HasPrefix(clean, "https://") {
		clean = "https://" + clean
	}
	return clean
}

// FetchSechnoUniversal mengambil data Terabox dari API sechno.com
func FetchSechnoUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	cleanURL := extractSechnoCleanURL(teraboxURL)
	if cleanURL == "" {
		return nil, fmt.Errorf("invalid terabox URL")
	}

	payload, err := json.Marshal(sechnoRequest{URL: cleanURL})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sechno.com/api/terabox", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://sechno.com/tools/terabox-downloader")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sechno request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sechno read body failed: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sechno HTTP %d: %s", resp.StatusCode, truncateString(string(raw), 200))
	}

	var data sechnoResponse
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("sechno invalid JSON: %w", err)
	}

	if !data.Success {
		msg := data.Error
		if msg == "" {
			msg = "failed to resolve Terabox link"
		}
		return nil, fmt.Errorf("sechno error: %s", msg)
	}

	if len(data.Files) == 0 {
		return nil, fmt.Errorf("sechno: no files found")
	}

	id, err := GenerateID(cleanURL)
	if err != nil {
		id = fmt.Sprintf("%d", time.Now().UnixNano()%10000000000)
	}

	var result []TeraboxUniversalData
	for _, f := range data.Files {
		if f.IsDir {
			continue
		}
		sizeStr := f.SizeFormatted
		if sizeStr == "" {
			sizeStr = fmt.Sprintf("%v B", f.Size)
		}

		result = append(result, TeraboxUniversalData{
			ID:          id,
			FileName:    f.Filename,
			FileSize:    sizeStr,
			Thumbnail:   f.ThumbURL,
			StreamURL:   f.StreamURL,
			DownloadURL: f.DownloadURL,
		})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("sechno: all items are directories or empty")
	}

	return result, nil
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
