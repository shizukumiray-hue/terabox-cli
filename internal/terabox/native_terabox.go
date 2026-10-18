package terabox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type nativeFileItem struct {
	Name     string
	Path     string
	Size     int64
	FsID     string
	IsDir    int
	Dlink    string
	Category string
}

type nativeShareResult struct {
	Errno   int
	ErrMsg  string
	Title   string
	ShareID int64
	Uk      int64
	Items   []nativeFileItem
}

var nativeSurlRegexes = []*regexp.Regexp{
	regexp.MustCompile(`/s/([A-Za-z0-9_-]+)`),
	regexp.MustCompile(`/(?:sharing/link|share/link)/([A-Za-z0-9_-]+)`),
	regexp.MustCompile(`[?&](?:surl|shorturl)=([A-Za-z0-9_-]+)`),
}

func extractNativeSurl(raw string) string {
	for _, re := range nativeSurlRegexes {
		if m := re.FindStringSubmatch(raw); len(m) > 1 {
			return m[1]
		}
	}
	return raw
}

var nativeHosts = []string{
	"terabox.com",
	"www.1024terabox.com",
	"www.terabox.app",
}

// FetchTeraboxNativeUniversal query langsung ke endpoint resmi Terabox (app_id=250528)
func FetchTeraboxNativeUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	surl := extractNativeSurl(teraboxURL)
	if surl == "" {
		return nil, fmt.Errorf("invalid terabox URL, surl not found")
	}

	cookie := os.Getenv("TERABOX_COOKIE")
	if cookie == "" {
		cookie = os.Getenv("TERABOX_NDUS")
		if cookie != "" && !strings.Contains(cookie, "ndus=") {
			cookie = "ndus=" + cookie
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}

	var candidates []string
	if strings.HasPrefix(surl, "1") {
		candidates = append(candidates, surl[1:], surl)
	} else {
		candidates = append(candidates, surl, "1"+surl)
	}

	var activeHost, activeSurl string
	var rootResp *nativeShareResult

	for _, cand := range candidates {
		for _, host := range nativeHosts {
			resp, err := queryNativeShareList(client, host, cand, "", 0, 0, cookie)
			if err == nil && resp.Errno == 0 {
				rootResp = resp
				activeHost = host
				activeSurl = cand
				break
			}
		}
		if rootResp != nil {
			break
		}
	}

	if rootResp == nil {
		return nil, fmt.Errorf("native terabox: failed to read share list")
	}

	var allItems []nativeFileItem
	traverseNativeFiles(client, activeHost, activeSurl, rootResp.ShareID, rootResp.Uk, cookie, rootResp.Items, &allItems)

	if len(allItems) == 0 {
		return nil, fmt.Errorf("native terabox: no files found")
	}

	id, err := GenerateID(teraboxURL)
	if err != nil {
		id = fmt.Sprintf("%d", time.Now().UnixNano()%10000000000)
	}

	var result []TeraboxUniversalData
	for _, it := range allItems {
		dlink := it.Dlink
		if dlink == "" {
			// Jika dlink kosong dan ada cookie, coba request share/download
			dlink, _ = resolveNativeDlink(client, activeHost, rootResp.ShareID, rootResp.Uk, it.FsID, cookie)
		}

		result = append(result, TeraboxUniversalData{
			ID:          id,
			FileName:    it.Name,
			FileSize:    FormatBytes(it.Size),
			Thumbnail:   "",
			StreamURL:   "",
			DownloadURL: dlink,
		})
	}

	return result, nil
}

func queryNativeShareList(client *http.Client, host, surl, dir string, shareID, uk int64, cookie string) (*nativeShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	apiURL := fmt.Sprintf("https://%s/share/list?app_id=250528&web=1&channel=dubox&clienttype=0&page=1&num=200&by=name&order=asc&shorturl=%s",
		host, url.QueryEscape(surl))

	if dir == "" {
		apiURL += "&root=1"
	} else {
		apiURL += fmt.Sprintf("&root=0&dir=%s&shareid=%d&uk=%d", url.QueryEscape(dir), shareID, uk)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", fmt.Sprintf("https://%s/", host))
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	errno := 0
	if e, ok := raw["errno"].(float64); ok {
		errno = int(e)
	}

	errmsg, _ := raw["errmsg"].(string)
	title, _ := raw["title"].(string)
	sID := toNativeInt64(raw["share_id"])
	ownerUk := toNativeInt64(raw["uk"])

	var items []nativeFileItem
	if listRaw, ok := raw["list"].([]interface{}); ok {
		for _, it := range listRaw {
			if m, ok := it.(map[string]interface{}); ok {
				items = append(items, parseNativeItem(m))
			}
		}
	}

	return &nativeShareResult{
		Errno:   errno,
		ErrMsg:  errmsg,
		Title:   title,
		ShareID: sID,
		Uk:      ownerUk,
		Items:   items,
	}, nil
}

func parseNativeItem(m map[string]interface{}) nativeFileItem {
	name, _ := m["server_filename"].(string)
	path, _ := m["path"].(string)
	dlink, _ := m["dlink"].(string)
	category, _ := m["category"].(string)

	isDir := 0
	switch v := m["isdir"].(type) {
	case float64:
		isDir = int(v)
	case string:
		if v == "1" {
			isDir = 1
		}
	}

	size := toNativeInt64(m["size"])
	fsID := ""
	switch v := m["fs_id"].(type) {
	case string:
		fsID = v
	case float64:
		fsID = fmt.Sprintf("%.0f", v)
	}

	return nativeFileItem{
		Name:     name,
		Path:     path,
		Size:     size,
		FsID:     fsID,
		IsDir:    isDir,
		Dlink:    dlink,
		Category: category,
	}
}

func toNativeInt64(v interface{}) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	case int64:
		return val
	}
	return 0
}

func traverseNativeFiles(client *http.Client, host, surl string, shareID, uk int64, cookie string, items []nativeFileItem, result *[]nativeFileItem) {
	for _, it := range items {
		if it.IsDir == 1 {
			subResp, err := queryNativeShareList(client, host, surl, it.Path, shareID, uk, cookie)
			if err == nil && subResp.Errno == 0 {
				traverseNativeFiles(client, host, surl, shareID, uk, cookie, subResp.Items, result)
			}
		} else {
			*result = append(*result, it)
		}
	}
}

func resolveNativeDlink(client *http.Client, host string, shareID, uk int64, fsID string, cookie string) (string, error) {
	if cookie == "" {
		return "", fmt.Errorf("cookie required")
	}

	dlURL := fmt.Sprintf("https://%s/share/download?app_id=250528&web=1&channel=dubox&clienttype=0&shareid=%d&uk=%d&primaryid=%d&fid_list=[%s]",
		host, shareID, uk, shareID, fsID)

	req, _ := http.NewRequest("GET", dlURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", fmt.Sprintf("https://%s/", host))
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if dlink, ok := res["dlink"].(string); ok && dlink != "" {
		return dlink, nil
	}
	return "", fmt.Errorf("dlink unavailable")
}
