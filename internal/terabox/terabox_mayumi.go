package terabox

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ========== KONSTANTA PRIVATE ==========
const (
	teraboxNDUS       = "YVpRgB8peHuioBg2od16nL6d818WSLZg1nbJ8Tuv"
	teraboxUSERID     = "320067180347"
	teraboxDEVUID     = "TBIMXV2-O_E58C50CDB6B04EF889B1B7B0FE0A57CB-C_0-D_99IEPI7RT-M_0068EB688271-V_8C6A2F6D"
	teraboxUSER_AGENT = "terabox;1.34.0.4;PC;PC-Windows;10.0.19045;WindowsTeraBox"
	proxyAPI          = "https://gabutproxy.xa507z7g.workers.dev/generate"

	// maxTeraboxWorkers membatasi konkurensi ke API Terabox & proxy stream.
	// ponytail: jadikan configurable via env kalau akun mulai kena rate-limit.
	maxTeraboxWorkers = 5
)

// teraboxNDUSValue memakai cookie ndus dari env kalau ada (mis. akun verified).
func teraboxNDUSValue() string {
	if v := os.Getenv("TERABOX_NDUS"); v != "" {
		return v
	}
	return teraboxNDUS
}

// ========== PRIVATE STRUCTURES ==========
type teraboxProxyResponse struct {
	Success   bool   `json:"success"`
	StreamURL string `json:"stream_url"`
	ExpiresIn string `json:"expires_in"`
	ID        string `json:"id"`
	Errno     int    `json:"errno"`
	Errmsg    string `json:"errmsg"`
	Message   string `json:"message"`
	Error     string `json:"error"`
}

type teraboxThumbnails struct {
	URL1 string `json:"url1,omitempty"`
	URL2 string `json:"url2,omitempty"`
	URL3 string `json:"url3,omitempty"`
	Icon string `json:"icon,omitempty"`
}

type teraboxFileData struct {
	ServerFilename string            `json:"server_filename"`
	MD5            string            `json:"md5"`
	EMD5           string            `json:"emd5"`
	Size           int64             `json:"size"`
	IsAdult        int               `json:"is_adult"`
	Dlink          string            `json:"dlink"`
	Thumbs         teraboxThumbnails `json:"thumbs"`
	FullPath       string            `json:"full_path"`
}

// ========== PRIVATE FUNCTIONS ==========
func teraboxSha1Hash(text string) string {
	h := sha1.New()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

func teraboxGenerateRand() (string, string) {
	salt := "Ng2sz6ktQahkvEkcKIhfak4WrM3r9a86"
	t := strconv.FormatInt(time.Now().Unix(), 10)
	ndusHash := teraboxSha1Hash(teraboxNDUSValue())
	payload := ndusHash + teraboxUSERID + salt + t + teraboxDEVUID
	randHash := teraboxSha1Hash(payload)
	return randHash, t
}

func teraboxExtractSurl(inputURL string, client *http.Client) string {
	if !strings.Contains(inputURL, "http") {
		return inputURL
	}
	req, _ := http.NewRequest("GET", inputURL, nil)
	req.Header.Set("User-Agent", teraboxUSER_AGENT)
	res, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	finalURL := res.Request.URL.String()
	re := regexp.MustCompile(`(?:surl=|/)([a-zA-Z0-9\-_]+)$`)
	match := re.FindStringSubmatch(finalURL)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func teraboxGenerateID(shortURL string) (string, error) {
	parsed, err := url.Parse(shortURL)
	if err != nil {
		return "", err
	}
	path := parsed.Path
	parts := strings.Split(path, "/s/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid terabox URL format, missing /s/")
	}
	code := parts[1]
	code = strings.Split(code, "?")[0]
	code = strings.Split(code, "/")[0]

	hash := md5.Sum([]byte(code))
	hexHash := hex.EncodeToString(hash[:])
	hexPart := hexHash[:8]
	val, err := strconv.ParseUint(hexPart, 16, 64)
	if err != nil {
		return "", err
	}
	idNum := val % 10000000000
	idStr := fmt.Sprintf("%010d", idNum)
	return idStr, nil
}

func teraboxFormatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %s", float64(bytes)/float64(div), []string{"KB", "MB", "GB", "TB"}[exp])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func teraboxGenerateStreamURL(dlinkURL string) (string, error) {
	// Body POST ke proxy stream: {"url": "<direct link terabox>"}.
	// Proxy yang mengubahnya jadi URL streamable untuk diputar langsung.
	payload := map[string]string{
		"url": dlinkURL,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", proxyAPI, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", teraboxUSER_AGENT)

	client := &http.Client{Timeout: 40 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy API HTTP %d: %s", res.StatusCode, firstLine(string(body)))
	}

	var proxyRes teraboxProxyResponse
	if err := json.Unmarshal(body, &proxyRes); err != nil {
		return "", fmt.Errorf("parse proxy response: %w, body: %s", err, firstLine(string(body)))
	}

	if !proxyRes.Success {
		detail := proxyRes.Errmsg
		if detail == "" {
			detail = proxyRes.Message
		}
		if detail == "" {
			detail = proxyRes.Error
		}
		if proxyRes.Errno != 0 {
			return "", fmt.Errorf("proxy API gagal: errno=%d errmsg=%s", proxyRes.Errno, detail)
		}
		return "", fmt.Errorf("proxy API returned success=false: %s", detail)
	}

	return proxyRes.StreamURL, nil
}

func teraboxGetInfoRaw(client *http.Client, surl string, path string) (string, error) {
	randStr, t := teraboxGenerateRand()
	surlCode := teraboxExtractSurl(surl, client)
	reqURL, _ := url.Parse("https://dm.terabox.com/share/list")
	q := reqURL.Query()
	q.Add("clienttype", "8")
	q.Add("channel", "0")
	q.Add("version", "1.34.0.4")
	q.Add("devuid", teraboxDEVUID)
	q.Add("rand", randStr)
	q.Add("time", t)
	q.Add("vip", "2")
	q.Add("lang", "en")
	q.Add("shorturl", surlCode)
	if path == "" {
		q.Add("root", "1")
	} else {
		q.Add("root", "0")
		q.Add("dir", path)
	}
	reqURL.RawQuery = q.Encode()
	req, _ := http.NewRequest("POST", reqURL.String(), nil)
	req.Header.Set("User-Agent", teraboxUSER_AGENT)
	req.AddCookie(&http.Cookie{Name: "ndus", Value: teraboxNDUSValue()})
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return string(body), nil
}

// teraboxResolveDlink menembak dlink mentah dan menangkap redirect 302 ke server CDN asli.
func teraboxResolveDlink(client *http.Client, dlink string) (string, error) {
	req, err := http.NewRequest("GET", dlink, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", teraboxUSER_AGENT)
	req.AddCookie(&http.Cookie{Name: "ndus", Value: teraboxNDUSValue()})

	// Buat client copy yang dilarang follow redirect (supaya kita bisa tangkap Location)
	clientCopy := *client
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	res, err := clientCopy.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	// Kalau Terabox merespon 302, ambil URL asli dari header Location
	if res.StatusCode == 302 || res.StatusCode == 301 {
		location := res.Header.Get("Location")
		if location != "" {
			return location, nil
		}
	}

	return "", fmt.Errorf("gagal resolve dlink, status: %d", res.StatusCode)
}

// teraboxListRaw ambil daftar item (file + folder) dari satu folder share.
func teraboxListRaw(client *http.Client, surl, path string) ([]map[string]interface{}, error) {
	raw, err := teraboxGetInfoRaw(client, surl, path)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("parse terabox JSON: %w", err)
	}
	if errno, ok := result["errno"].(float64); ok && errno != 0 {
		msg, _ := result["errmsg"].(string)
		return nil, fmt.Errorf("terabox API error: errno=%d errmsg=%s", int(errno), msg)
	}
	list, ok := result["list"].([]interface{})
	if !ok {
		return nil, nil
	}
	items := make([]map[string]interface{}, 0, len(list))
	for _, itemRaw := range list {
		if item, ok := itemRaw.(map[string]interface{}); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func teraboxIsDir(item map[string]interface{}) bool {
	switch v := item["isdir"].(type) {
	case string:
		return v == "1"
	case float64:
		return v == 1
	}
	return false
}

func teraboxItemToFileData(item map[string]interface{}, currentPath string) teraboxFileData {
	serverFilename, _ := item["server_filename"].(string)
	md5, _ := item["md5"].(string)
	emd5, _ := item["emd5"].(string)
	size, _ := item["size"].(float64)
	isAdult, _ := item["is_adult"].(float64)
	dlink, _ := item["dlink"].(string)

	thumbs := teraboxThumbnails{}
	if thumbsRaw, ok := item["thumbs"].(map[string]interface{}); ok {
		if u1, ok := thumbsRaw["url1"].(string); ok {
			thumbs.URL1 = u1
		}
		if u2, ok := thumbsRaw["url2"].(string); ok {
			thumbs.URL2 = u2
		}
		if u3, ok := thumbsRaw["url3"].(string); ok {
			thumbs.URL3 = u3
		}
		if icon, ok := thumbsRaw["icon"].(string); ok {
			thumbs.Icon = icon
		}
	}

	fullPath := currentPath
	if apiPath, ok := item["path"].(string); ok && apiPath != "" {
		fullPath = apiPath
	} else if fullPath == "" {
		fullPath = "/" + serverFilename
	} else {
		fullPath = fullPath + "/" + serverFilename
	}

	return teraboxFileData{
		ServerFilename: serverFilename,
		MD5:            md5,
		EMD5:           emd5,
		Size:           int64(size),
		IsAdult:        int(isAdult),
		Dlink:          dlink,
		Thumbs:         thumbs,
		FullPath:       fullPath,
	}
}

// teraboxCollectFiles kumpulkan semua file dari share dengan listing folder paralel.
// Jumlah worker mengikuti jumlah folder level-1 (dibatasi maxTeraboxWorkers).
func teraboxCollectFiles(client *http.Client, surl string) []teraboxFileData {
	rootItems, err := teraboxListRaw(client, surl, "")
	if err != nil {
		logError("Error fetching terabox root", zap.String("path", ""), zap.Error(err))
		return nil
	}

	var files []teraboxFileData
	var rootFolders []string
	for _, item := range rootItems {
		if teraboxIsDir(item) {
			if sub, ok := item["path"].(string); ok && sub != "" {
				rootFolders = append(rootFolders, sub)
			}
		} else {
			files = append(files, teraboxItemToFileData(item, ""))
		}
	}

	if len(rootFolders) == 0 {
		return files
	}

	workers := len(rootFolders)
	if workers > maxTeraboxWorkers {
		workers = maxTeraboxWorkers
	}

	type folderTask struct{ path string }
	tasks := make(chan folderTask, maxTeraboxWorkers*4)

	var mu sync.Mutex
	var pending int
	// schedule non-blocking: overflow dilempar ke goroutine supaya worker
	// tidak pernah saling blokir send dan mati deadlock.
	schedule := func(p string) {
		mu.Lock()
		pending++
		mu.Unlock()
		select {
		case tasks <- folderTask{path: p}:
		default:
			go func() { tasks <- folderTask{path: p} }()
		}
	}
	finish := func() {
		mu.Lock()
		pending--
		done := pending == 0
		mu.Unlock()
		if done {
			close(tasks)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logError("Panic di terabox-folder-walker",
						zap.Any("recover", r), zap.Stack("stack"))
					finish()
				}
			}()
			for t := range tasks {
				items, err := teraboxListRaw(client, surl, t.path)
				if err != nil {
					logError("Error fetching terabox folder",
						zap.String("path", t.path),
						zap.Error(err))
					finish()
					continue
				}
				for _, item := range items {
					if teraboxIsDir(item) {
						if sub, ok := item["path"].(string); ok && sub != "" {
							schedule(sub)
						}
					} else {
						mu.Lock()
						files = append(files, teraboxItemToFileData(item, t.path))
						mu.Unlock()
					}
				}
				finish()
			}
		}()
	}

	for _, p := range rootFolders {
		schedule(p)
	}
	wg.Wait()
	return files
}

// teraboxEnrichFiles lengkapi tiap file: stream URL (via proxy) + download URL, paralel.
// Kandidat stream: located dlink (direct, tanpa verifikasi akun) dulu, baru dlink raw.
func teraboxEnrichFiles(client *http.Client, teraboxURL string, files []teraboxFileData) []TeraboxUniversalData {
	id, err := teraboxGenerateID(teraboxURL)
	if err != nil {
		logWarn("Gagal generate ID", zap.Error(err))
		id = "unknown"
	}

	results := make([]TeraboxUniversalData, len(files))
	if len(files) == 0 {
		return results
	}

	workers := len(files)
	if workers > maxTeraboxWorkers {
		workers = maxTeraboxWorkers
	}

	idxCh := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logError("Panic di terabox-enrich",
						zap.Any("recover", r), zap.Stack("stack"))
				}
			}()
			for idx := range idxCh {
				file := files[idx]

				// Kandidat URL untuk stream, maksimal 2: located dlink lalu dlink raw.
				var candidates []string
				if file.Dlink != "" {
					if resolved, err := teraboxResolveDlink(client, file.Dlink); err == nil && resolved != "" {
						candidates = append(candidates, resolved)
					}
					// Fallback kalau gagal resolve, pakai dlink mentah
					candidates = append(candidates, file.Dlink)
				}

				streamURL := ""
				var lastStreamErr error
				for _, cand := range candidates {
					s, err := teraboxGenerateStreamURL(cand)
					if err == nil && s != "" {
						streamURL = s
						break
					}
					lastStreamErr = err
					logWarn("Gagal generate stream URL",
						zap.String("filename", file.ServerFilename),
						zap.String("candidate", cand),
						zap.Error(err))
				}
				if streamURL == "" && lastStreamErr != nil {
					logWarn("Semua kandidat stream gagal",
						zap.String("filename", file.ServerFilename),
						zap.Error(lastStreamErr))
				}

				downloadURL := file.Dlink
				if downloadURL == "" && len(candidates) > 0 {
					downloadURL = candidates[0]
				}

				results[idx] = TeraboxUniversalData{
					ID:          id,
					FileName:    file.ServerFilename,
					FileSize:    teraboxFormatFileSize(file.Size),
					Thumbnail:   file.Thumbs.URL3,
					StreamURL:   streamURL,
					DownloadURL: downloadURL,
				}
			}
		}()
	}

	for i := range files {
		idxCh <- i
	}
	close(idxCh)
	wg.Wait()
	return results
}

// ========== PUBLIC FUNCTION ==========
// FetchTeraboxDirectUniversal adalah API adapter untuk terabox extractor
// Fungsi ini akan dipanggil oleh FetchTeraboxUniversal sebagai salah satu pilihan API
func FetchTeraboxDirectUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	client := &http.Client{Timeout: 40 * time.Second}

	logInfo("Mengekstrak files dari terabox", zap.String("url", teraboxURL))

	files := teraboxCollectFiles(client, teraboxURL)
	if len(files) == 0 {
		return nil, fmt.Errorf("tidak ada file ditemukan")
	}

	logInfo("Files berhasil dikumpulkan", zap.Int("total", len(files)))

	result := teraboxEnrichFiles(client, teraboxURL, files)
	logInfo("Konversi ke universal format selesai", zap.Int("total", len(result)))
	return result, nil
}
