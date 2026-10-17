package terabox

// TeraboxUniversalData adalah struct output universal untuk semua sumber API
type TeraboxUniversalData struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	FileSize    string `json:"file_size"`
	Thumbnail   string `json:"thumbnail"`
	StreamURL   string `json:"stream_url"`
	DownloadURL string `json:"download_url"`
}
