package terabox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL     = "https://iteraplay.com"
	apiEndpoint = "/api/download"
)

// Request body untuk /api/download
type downloadRequest struct {
	URL string `json:"url"`
}

// Response dari /api/download
type downloadResponse struct {
	Status        string `json:"status"`
	TotalFiles    int    `json:"total_files"`
	TotalFolders  int    `json:"total_folders"`
	FolderZipLink string `json:"folder_zip_dlink"`
	List          []File `json:"list"`
}

// Struktur setiap file
type File struct {
	FsID          int64             `json:"fs_id"`
	Name          string            `json:"name"`
	Size          int64             `json:"size"`
	SizeFormatted string            `json:"size_formatted"`
	Type          string            `json:"type"`
	IsDir         string            `json:"is_dir"` // "0" = file, "1" = folder
	Duration      string            `json:"duration"`
	Quality       string            `json:"quality"`
	NormalDLink   string            `json:"normal_dlink"`
	ZipDLink      string            `json:"zip_dlink"`
	FastStreamURL map[string]string `json:"fast_stream_url"`
	SubtitleURL   string            `json:"subtitle_url"`
	Thumbnail     string            `json:"thumbnail"`
	Folder        string            `json:"folder"`
}

// Output yang diminta
type OutputFile struct {
	FileName       string `json:"file_name"`
	FileSize       string `json:"file_size"`
	FileSizeBytes  int64  `json:"file_size_bytes"`
	Thumbnail      string `json:"thumbnail"`
	StreamFinalURL string `json:"stream_final_url"` // resolusi tertinggi
	DownloadURL    string `json:"download_url"`     // normal_dlink
}

// Ambil cookie dari halaman utama dan kembalikan client dengan cookie jar
func getClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 40 * time.Second,
	}

	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="147", "Not.A/Brand";v="8", "Chromium";v="147"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Linux"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return client, nil
}

// Kirim POST request ke /api/download
func fetchFiles(client *http.Client, teraboxURL string) (*downloadResponse, error) {
	body := downloadRequest{URL: teraboxURL}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURL+apiEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="147", "Not.A/Brand";v="8", "Chromium";v="147"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Linux"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result downloadResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse JSON error: %v, body: %s", err, string(respBody))
	}
	return &result, nil
}

// Pilih resolusi tertinggi dari fast_stream_url
func getHighestQuality(streams map[string]string) string {
	if len(streams) == 0 {
		return ""
	}
	type quality struct {
		key   string
		value int
	}
	var qList []quality
	for k := range streams {
		numStr := strings.TrimSuffix(k, "p")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			num = 0
		}
		qList = append(qList, quality{key: k, value: num})
	}
	sort.Slice(qList, func(i, j int) bool {
		return qList[i].value > qList[j].value
	})
	return streams[qList[0].key]
}

// Konversi File ke OutputFile
func toOutputFile(f File) OutputFile {
	streamURL := getHighestQuality(f.FastStreamURL)
	return OutputFile{
		FileName:       f.Name,
		FileSize:       f.SizeFormatted,
		FileSizeBytes:  f.Size,
		Thumbnail:      f.Thumbnail,
		StreamFinalURL: streamURL,
		DownloadURL:    f.NormalDLink,
	}
}

// ToUniversal mengkonversi OutputFile ke TeraboxUniversalData
func (of OutputFile) ToUniversal(teraboxURL string) (TeraboxUniversalData, error) {
	id, err := GenerateID(teraboxURL)
	if err != nil {
		return TeraboxUniversalData{}, err
	}
	return TeraboxUniversalData{
		ID:          id,
		FileName:    of.FileName,
		FileSize:    of.FileSize,
		Thumbnail:   of.Thumbnail,
		StreamURL:   of.StreamFinalURL,
		DownloadURL: of.DownloadURL,
	}, nil
}

// FetchIteraMedia adalah fungsi utama yang bisa kamu export/import ke file lain
func FetchIteraMedia(teraboxURL string) ([]OutputFile, error) {
	client, err := getClient()
	if err != nil {
		return nil, fmt.Errorf("gagal mendapatkan client/cookie: %w", err)
	}

	resp, err := fetchFiles(client, teraboxURL)
	if err != nil {
		return nil, fmt.Errorf("gagal mengambil data API: %w", err)
	}

	if resp.Status != "success" {
		return nil, fmt.Errorf("API mengembalikan status error: %s", resp.Status)
	}

	var outputs []OutputFile
	for _, file := range resp.List {
		// Hanya proses file (is_dir == "0"), abaikan folder
		if file.IsDir == "0" {
			outputs = append(outputs, toOutputFile(file))
		}
	}

	return outputs, nil
}

// FetchIteraMediaUniversal mengambil data dari iteraplay dan mengembalikan dalam bentuk universal
func FetchIteraMediaUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	outputs, err := FetchIteraMedia(teraboxURL)
	if err != nil {
		return nil, err
	}
	var result []TeraboxUniversalData
	for _, out := range outputs {
		uni, err := out.ToUniversal(teraboxURL)
		if err != nil {
			return nil, err // bisa juga continue jika ingin toleran error per file
		}
		result = append(result, uni)
	}
	return result, nil
}
