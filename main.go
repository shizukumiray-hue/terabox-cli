package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/joho/godotenv"
	"github.com/valyala/fasthttp"

	"terabox-cli/internal/terabox"
)

var version = "dev"

const (
	maxTelegramFileSize = 2000 * 1024 * 1024 // 2 GB
	banner              = `
========================================================================================
   ______                 ____               ________    ____
  /_  __/__  _________ _ / __ )____  _  __  / ____/ /   /  _/
   / / / _ \/ ___/ __ ` + "`" + `// __  / __ \| |/_/ / /   / /    / /  
  / / /  __/ /  / /_/ // /_/ / /_/ />  <  / /___/ /____/ /   
 /_/  \___/_/   \__,_//_____/\____/_/|_|  \____/_____/___/   
========================================================================================
           Zero-Disk Streamer: TeraBox -> MTProto Telegram (High-Speed fasthttp)
========================================================================================`
)

// ANSI color escape codes
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan    = "\033[36m"
	colorMagenta = "\033[35m"
	colorWhite   = "\033[37m"
)

var fastHTTPClient = &fasthttp.Client{
	Name:                     "terabox-cli-streamer",
	ReadTimeout:              10 * time.Minute,
	WriteTimeout:             60 * time.Second,
	MaxConnsPerHost:          256,
	MaxIdleConnDuration:      90 * time.Second,
	NoDefaultUserAgentHeader: true,
	TLSConfig: &tls.Config{
		InsecureSkipVerify: true,
	},
}

func main() {
	// 1. Muat environment dari .env
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../../.env")

	apiIDStr := os.Getenv("TELEGRAM_API_ID")
	apiHash := os.Getenv("TELEGRAM_API_HASH")
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	targetIDStr := os.Getenv("TARGET_USER_ID")
	if targetIDStr == "" {
		targetIDStr = os.Getenv("OWNER_ID")
	}

	if apiIDStr == "" || apiHash == "" || botToken == "" || targetIDStr == "" {
		fmt.Fprintf(os.Stderr, "%s❌ Error:%s TELEGRAM_API_ID, TELEGRAM_API_HASH, TELEGRAM_BOT_TOKEN, dan TARGET_USER_ID harus ada di .env\n", colorRed, colorReset)
		os.Exit(1)
	}

	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s❌ Error:%s TELEGRAM_API_ID tidak valid: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	targetUserID, err := strconv.ParseInt(targetIDStr, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s❌ Error:%s ID Telegram target tidak valid: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	// Cetak banner ASCII awal
	printBanner()

	// Inisialisasi GOTD client sekali untuk seluruh sesi REPL
	sessPath := "session_terabox.json"
	if _, err := os.Stat("session.json"); err == nil {
		sessPath = "session.json"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: sessPath},
	})

	err = client.Run(ctx, func(ctx context.Context) error {
		// Autentikasi Bot
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("status auth error: %w", err)
		}
		if !status.Authorized {
			fmt.Printf("%s⏳ Mengautentikasi bot token ke Telegram...%s\n", colorYellow, colorReset)
			for {
				_, err := client.Auth().Bot(ctx, botToken)
				if err == nil {
					break
				}
				if d, ok := tgerr.AsFloodWait(err); ok {
					fmt.Printf("%s⚠️ Flood wait auth, menunggu %v...%s\n", colorYellow, d, colorReset)
					select {
					case <-time.After(d + time.Second):
						continue
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return fmt.Errorf("bot login failed: %w", err)
			}
		}

		// Dapatkan Peer Target User
		users, err := client.API().UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: targetUserID}})
		if err != nil || len(users) == 0 {
			return fmt.Errorf("gagal mendapatkan target user ID %d (pastikan user tersebut sudah pernah klik /start ke bot): %w", targetUserID, err)
		}

		u, ok := users[0].(*tg.User)
		if !ok {
			return fmt.Errorf("peer %d bukan tipe User (pastikan user sudah pernah kirim /start ke bot)", targetUserID)
		}
		peer := &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
		up := uploader.NewUploader(client.API()).WithThreads(2)

		fmt.Printf("%s✅ Bot terautentikasi & siap melayani target ID %d!%s\n", colorGreen, targetUserID, colorReset)

		reader := bufio.NewReader(os.Stdin)

		// Cek apakah ada argumen awal dari os.Args
		if len(os.Args) > 1 {
			initialArg := strings.TrimSpace(os.Args[1])
			if initialArg != "" {
				fmt.Printf("\n%s🚀 Memproses argumen awal:%s %s\n", colorCyan, colorReset, initialArg)
				processURL(ctx, client, up, peer, targetUserID, initialArg, reader)
			}
		}

		// REPL Interactive CLI Loop
		printHelp()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			fmt.Printf("\n%s%sterabox>%s ", colorBold, colorCyan, colorReset)
			line, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					fmt.Println("\nKeluar...")
					return nil
				}
				fmt.Printf("%s❌ Error input: %v%s\n", colorRed, err, colorReset)
				continue
			}

			input := strings.TrimSpace(line)
			if input == "" {
				continue
			}

			lowerInput := strings.ToLower(input)

			switch {
			case lowerInput == "exit" || lowerInput == "quit":
				fmt.Printf("%s👋 Keluar dari aplikasi. Sampai jumpa!%s\n", colorGreen, colorReset)
				return nil

			case lowerInput == "clear":
				fmt.Print("\033[H\033[2J")
				printBanner()

			case lowerInput == "help":
				printHelp()

			case strings.HasPrefix(lowerInput, "url "):
				targetURL := strings.TrimSpace(input[4:])
				if targetURL == "" {
					fmt.Printf("%s❌ Format salah. Gunakan: url <link>%s\n", colorRed, colorReset)
					continue
				}
				processURL(ctx, client, up, peer, targetUserID, targetURL, reader)

			case strings.HasPrefix(lowerInput, "http://") || strings.HasPrefix(lowerInput, "https://"):
				processURL(ctx, client, up, peer, targetUserID, input, reader)

			default:
				fmt.Printf("%s❌ Perintah tidak dikenal: '%s'. Ketik 'help' untuk daftar perintah atau langsung paste link URL.%s\n", colorYellow, input, colorReset)
			}
		}
	})

	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "\n%s❌ Sesi bot berakhir dengan error: %v%s\n", colorRed, err, colorReset)
	}
}

func printBanner() {
	fmt.Printf("%s%s%s\n", colorCyan, banner, colorReset)
	fmt.Printf("               %sVersi: %s%s\n\n", colorBold, version, colorReset)
}

func printHelp() {
	fmt.Printf("\n%s%sDaftar Perintah Interaktif:%s\n", colorBold, colorWhite, colorReset)
	fmt.Printf("  %surl <link>%s         : Proses tautan TeraBox\n", colorGreen, colorReset)
	fmt.Printf("  %s<link langsung>%s    : Paste link TeraBox langsung (http/https)\n", colorGreen, colorReset)
	fmt.Printf("  %sclear%s              : Bersihkan layar terminal\n", colorYellow, colorReset)
	fmt.Printf("  %shelp%s               : Tampilkan bantuan perintah ini\n", colorYellow, colorReset)
	fmt.Printf("  %sexit / quit%s        : Keluar dari aplikasi\n", colorRed, colorReset)
}

func renderFilesTable(files []terabox.TeraboxUniversalData) {
	fmt.Println(strings.Repeat("=", 88))
	fmt.Printf("%s%-4s | %-42s | %-12s | %s%s\n", colorBold, "No", "Nama Berkas", "Ukuran", "Status Link", colorReset)
	fmt.Println(strings.Repeat("-", 88))

	for i, f := range files {
		hasLink := fmt.Sprintf("%s✅ Tersedia%s", colorGreen, colorReset)
		if f.DownloadURL == "" && f.StreamURL == "" {
			hasLink = fmt.Sprintf("%s⚠️ Perlu Akun%s", colorYellow, colorReset)
		}
		name := f.FileName
		if len(name) > 42 {
			name = name[:39] + "..."
		}
		fmt.Printf("%-4d | %-42s | %-12s | %s\n", i+1, name, f.FileSize, hasLink)
	}
	fmt.Println(strings.Repeat("=", 88))
}

func processURL(
	ctx context.Context,
	client *telegram.Client,
	up *uploader.Uploader,
	peer *tg.InputPeerUser,
	targetUserID int64,
	inputURL string,
	reader *bufio.Reader,
) {
	fmt.Printf("\n%s⏳ Mengekstrak media via Circuit Breaker (Komiktap/Sechno/Mayumi/Iteraplay/Flow/Netlify/Native)...%s\n", colorYellow, colorReset)
	files, err := terabox.FetchTeraboxUniversal(inputURL)
	if err != nil || len(files) == 0 {
		fmt.Printf("%s❌ Gagal mengambil data Terabox: %v%s\n", colorRed, err, colorReset)
		return
	}

	fmt.Printf("\n%s✅ Berhasil! Ditemukan %d berkas:%s\n", colorGreen, len(files), colorReset)
	renderFilesTable(files)

	fmt.Printf("\n%s📥 Pilih berkas (contoh: 1 atau 1,3,5 atau 1-5 atau 'all'/'semua', 'c' untuk batalkan):%s ", colorBold, colorReset)
	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	if strings.ToLower(choiceStr) == "c" || choiceStr == "" {
		fmt.Printf("%s⚠️ Pemilihan berkas dibatalkan.%s\n", colorYellow, colorReset)
		return
	}

	selectedIndices := parseSelection(choiceStr, len(files))
	if len(selectedIndices) == 0 {
		fmt.Printf("%s❌ Pilihan tidak valid atau dibatalkan.%s\n", colorRed, colorReset)
		return
	}

	fmt.Printf("\n%s🚀 Memulai pengiriman %d berkas ke Telegram (Target: %d) via Zero-Disk fasthttp streaming...%s\n",
		colorCyan, len(selectedIndices), targetUserID, colorReset)

	for count, idx := range selectedIndices {
		select {
		case <-ctx.Done():
			fmt.Printf("\n%s⚠️ Proses dibatalkan oleh sinyal interupsi.%s\n", colorYellow, colorReset)
			return
		default:
		}

		fileTarget := files[idx]
		dlURL := fileTarget.DownloadURL
		if dlURL == "" {
			dlURL = fileTarget.StreamURL
		}

		if dlURL == "" {
			fmt.Printf("[%d/%d] %s⚠️ Lewati %s: Direct link tidak tersedia.%s\n",
				count+1, len(selectedIndices), colorYellow, fileTarget.FileName, colorReset)
			continue
		}

		fmt.Printf("\n[%d/%d] %s🌐 Menghubungkan stream: %s%s\n",
			count+1, len(selectedIndices), colorCyan, fileTarget.FileName, colorReset)

		streamSafe, totalSize, err := openStreamWithRetry(ctx, dlURL, 3)
		if err != nil {
			fmt.Printf("%s❌ Gagal mengunduh '%s' setelah 3 kali percobaan: %v. Lanjut ke file berikutnya.%s\n",
				colorRed, fileTarget.FileName, err, colorReset)
			continue
		}

		// Validasi batas Telegram 2 GB
		if totalSize > maxTelegramFileSize {
			_ = streamSafe.Close()
			fmt.Printf("%s⚠️ File '%s' melebihi batas 2 GB (%s). Lewati upload.%s\n",
				colorYellow, fileTarget.FileName, formatByteSize(totalSize), colorReset)
			continue
		}

		var bodyReader io.Reader = streamSafe
		if totalSize < 0 {
			bodyReader = io.LimitReader(streamSafe, maxTelegramFileSize+1)
		}

		// MAGICPIPE: intip byte awal stream untuk deteksi MIME asli via mimetype (Zero-Disk)
		detectedMime, magicStream, err := detectMagicPipe(bodyReader, fileTarget.FileName)
		if err != nil {
			fmt.Printf("   %s⚠️ Gagal deteksi magic bytes: %v. Gunakan ekstensi bawaan.%s\n", colorYellow, err, colorReset)
		} else {
			fmt.Printf("   %s🔮 MagicPipe MIME:%s %s%s%s\n", colorMagenta, colorReset, colorBold, detectedMime, colorReset)
		}

		pr := &progressReader{
			reader:    magicStream,
			total:     totalSize,
			name:      fileTarget.FileName,
			startTime: time.Now(),
		}

		fmt.Printf("%s⏳ Mengunggah langsung ke Telegram via fasthttp stream (Zero Disk)...%s\n", colorDim, colorReset)
		uploadFile, err := up.Upload(ctx, uploader.NewUpload(fileTarget.FileName, pr, totalSize))
		_ = streamSafe.Close()

		if err != nil {
			fmt.Printf("\n%s❌ Gagal mengunggah ke Telegram: %v%s\n", colorRed, err, colorReset)
			continue
		}

		fmt.Printf("\n%s✅ Berhasil diunggah ke server Telegram. Mengirim pesan media...%s\n", colorGreen, colorReset)

		tgAttributes := buildTelegramAttributes(fileTarget.FileName, detectedMime)

		captionHTML := fmt.Sprintf(
			"📁 <b>%s</b>\n<blockquote expandable>⚖️ <b>Ukuran:</b> <code>%s</code>\n🔮 <b>Format:</b> <code>%s</code>\n⚡ <b>Engine:</b> <code>TeraBox FastStream (MagicPipe)</code>\n🤖 <b>Platform:</b> <i>TeraBox CLI</i></blockquote>",
			htmlEscape(fileTarget.FileName),
			htmlEscape(fileTarget.FileSize),
			htmlEscape(detectedMime),
		)
		msgText, entities := parseCaptionHTML(captionHTML)
		docReq := &tg.MessagesSendMediaRequest{
			Peer: peer,
			Media: &tg.InputMediaUploadedDocument{
				File:       uploadFile,
				MimeType:   detectedMime,
				Attributes: tgAttributes,
			},
			Message:  msgText,
			Entities: entities,
			RandomID: time.Now().UnixNano(),
		}

		sent := false
		for {
			_, err = client.API().MessagesSendMedia(ctx, docReq)
			if err == nil {
				fmt.Printf("%s✅ [OK] Berkas '%s' sukses terkirim ke user %d!%s\n", colorGreen, fileTarget.FileName, targetUserID, colorReset)
				sent = true
				break
			}
			if d, ok := tgerr.AsFloodWait(err); ok {
				fmt.Printf("%s⚠️ Flood wait kirim pesan, istirahat %v...%s\n", colorYellow, d, colorReset)
				select {
				case <-time.After(d + time.Second):
					continue
				case <-ctx.Done():
					return
				}
			}
			fmt.Printf("%s❌ Gagal kirim media ke chat: %v%s\n", colorRed, err, colorReset)
			break
		}
		if !sent {
			fmt.Printf("%s⚠️ Media '%s' tidak terkirim.%s\n", colorYellow, fileTarget.FileName, colorReset)
		}
	}

	sender := message.NewSender(client.API())
	summaryHTML := fmt.Sprintf(
		"🎉 <b>Pengiriman Selesai!</b>\n<blockquote>Berhasil mengirim <code>%d</code> berkas ke chat via <b>TeraBox CLI</b>.</blockquote>",
		len(selectedIndices),
	)
	_, _ = sender.To(peer).StyledText(ctx, html.String(nil, summaryHTML))

	fmt.Printf("\n%s🎉 Selesai memproses tautan TeraBox! Siap menerima perintah berikutnya.%s\n", colorGreen, colorReset)
}

// openStreamWithRetry membuka fasthttp connection stream dengan exponential backoff retry
func openStreamWithRetry(ctx context.Context, dlURL string, maxRetries int) (io.ReadCloser, int64, error) {
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		rc, totalSize, statusCode, err := executeFastHTTPStreamPipe(ctx, dlURL)
		if err == nil && statusCode >= 200 && statusCode < 300 {
			return rc, totalSize, nil
		}
		if rc != nil {
			_ = rc.Close()
		}

		if err != nil {
			lastErr = err
			fmt.Printf("   %s⚠️ [Percobaan %d/%d] Network/Stream error: %v%s\n", colorYellow, attempt, maxRetries, err, colorReset)
		} else {
			lastErr = fmt.Errorf("server return status HTTP %d", statusCode)
			fmt.Printf("   %s⚠️ [Percobaan %d/%d] Server mengembalikan HTTP %d%s\n", colorYellow, attempt, maxRetries, statusCode, colorReset)
			if statusCode < 500 && statusCode != 429 && statusCode != 408 {
				// Bukan error 5xx / timeout / rate limit, batalkan retry
				return nil, 0, lastErr
			}
		}

		if attempt < maxRetries {
			fmt.Printf("   %s⏳ Menunggu %v sebelum mencoba lagi...%s\n", colorDim, backoff, colorReset)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			}
			backoff *= 2
		}
	}

	return nil, 0, fmt.Errorf("gagal setelah %d percobaan: %w", maxRetries, lastErr)
}

// executeFastHTTPStreamPipe mengalirkan response body fasthttp ke io.ReadCloser (zero-disk)
func executeFastHTTPStreamPipe(ctx context.Context, dlURL string) (io.ReadCloser, int64, int, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	req.SetRequestURI(dlURL)
	req.Header.SetMethod(fasthttp.MethodGet)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://www.terabox.com/")
	if ndus := os.Getenv("TERABOX_NDUS"); ndus != "" {
		req.Header.Set("Cookie", "ndus="+strings.TrimPrefix(ndus, "ndus="))
	}

	type streamInitResult struct {
		statusCode    int
		contentLength int64
		err           error
	}
	initChan := make(chan streamInitResult, 1)

	pr, pw := io.Pipe()

	go func() {
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)

		var err error
		if dl, ok := ctx.Deadline(); ok {
			err = fastHTTPClient.DoDeadline(req, resp, dl)
		} else {
			err = fastHTTPClient.Do(req, resp)
		}

		if err != nil {
			initChan <- streamInitResult{err: err}
			_ = pw.CloseWithError(err)
			return
		}

		statusCode := resp.StatusCode()
		contentLength := int64(resp.Header.ContentLength())

		initChan <- streamInitResult{
			statusCode:    statusCode,
			contentLength: contentLength,
		}

		if statusCode < 200 || statusCode >= 300 {
			_ = pw.Close()
			return
		}

		bodyStream := resp.BodyStream()
		if bodyStream != nil {
			_, copyErr := io.Copy(pw, bodyStream)
			if copyErr != nil {
				_ = pw.CloseWithError(copyErr)
			} else {
				_ = pw.Close()
			}
		} else {
			bodyBytes := resp.Body()
			if len(bodyBytes) > 0 {
				_, _ = pw.Write(bodyBytes)
			}
			_ = pw.Close()
		}
	}()

	select {
	case <-ctx.Done():
		_ = pr.Close()
		_ = pw.CloseWithError(ctx.Err())
		return nil, 0, 0, ctx.Err()
	case res := <-initChan:
		if res.err != nil {
			_ = pr.Close()
			return nil, 0, 0, res.err
		}
		if res.statusCode < 200 || res.statusCode >= 300 {
			_ = pr.Close()
			return nil, res.contentLength, res.statusCode, nil
		}
		return pr, res.contentLength, res.statusCode, nil
	}
}

// progressReader memantau aliran byte langsung dari response stream ke Telegram tanpa disk
type progressReader struct {
	reader    io.Reader
	total     int64
	read      int64
	name      string
	lastP     int
	startTime time.Time
	lastTime  time.Time
	lastRead  int64
	currSpeed string
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		now := time.Now()

		if pr.lastTime.IsZero() || now.Sub(pr.lastTime) >= 500*time.Millisecond {
			if !pr.lastTime.IsZero() {
				diffBytes := pr.read - pr.lastRead
				diffSec := now.Sub(pr.lastTime).Seconds()
				if diffSec > 0 {
					speed := float64(diffBytes) / diffSec
					pr.currSpeed = fmt.Sprintf("%s/s", formatByteSize(int64(speed)))
				}
			}
			pr.lastTime = now
			pr.lastRead = pr.read
		}

		speedText := pr.currSpeed
		if speedText == "" {
			speedText = "menghitung..."
		}

		if pr.total > 0 {
			pct := int(float64(pr.read) / float64(pr.total) * 100)
			if pct != pr.lastP {
				pr.lastP = pct
				fmt.Printf("\r\033[K%s    --> 🚀 Streaming: %s / %s (%d%%) | Kecepatan: %s%s",
					colorCyan, formatByteSize(pr.read), formatByteSize(pr.total), pct, speedText, colorReset)
			}
		} else {
			fmt.Printf("\r\033[K%s    --> 🚀 Streaming: %s | Kecepatan: %s%s",
				colorCyan, formatByteSize(pr.read), speedText, colorReset)
		}
	}
	return n, err
}

func formatByteSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func parseSelection(input string, total int) []int {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "all" || input == "semua" || input == "*" {
		indices := make([]int, total)
		for i := 0; i < total; i++ {
			indices[i] = i
		}
		return indices
	}

	selectedMap := make(map[int]bool)
	parts := strings.Split(input, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) == 2 {
				start, err1 := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
				end, err2 := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
				if err1 == nil && err2 == nil && start <= end {
					for i := start; i <= end; i++ {
						if i >= 1 && i <= total {
							selectedMap[i-1] = true
						}
					}
				}
			}
		} else {
			val, err := strconv.Atoi(part)
			if err == nil && val >= 1 && val <= total {
				selectedMap[val-1] = true
			}
		}
	}

	var result []int
	for idx := range selectedMap {
		result = append(result, idx)
	}
	sort.Ints(result)
	return result
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func parseCaptionHTML(rawHTML string) (string, []tg.MessageEntityClass) {
	var b entity.Builder
	if err := html.HTML(strings.NewReader(rawHTML), &b, html.Options{}); err != nil {
		return rawHTML, nil
	}
	return b.Complete()
}

// detectMagicPipe mengintip bit header dari stream tanpa menguras stream asli (Zero-Disk)
func detectMagicPipe(r io.Reader, filename string) (string, io.Reader, error) {
	header := make([]byte, 3072)
	n, err := io.ReadAtLeast(r, header, 1)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "application/octet-stream", r, err
	}

	header = header[:n]
	mimeResult := mimetype.Detect(header)
	detectedMime := mimeResult.String()

	if detectedMime == "" || detectedMime == "application/octet-stream" {
		if extMime := mime.TypeByExtension(filepath.Ext(filename)); extMime != "" {
			detectedMime = extMime
		} else {
			detectedMime = "application/octet-stream"
		}
	}

	fullStream := io.MultiReader(bytes.NewReader(header), r)
	return detectedMime, fullStream, nil
}

// buildTelegramAttributes mengatur bentuk pengiriman dokumen/video/audio di Telegram
func buildTelegramAttributes(filename, mimeType string) []tg.DocumentAttributeClass {
	attrs := []tg.DocumentAttributeClass{
		&tg.DocumentAttributeFilename{FileName: filename},
	}

	switch {
	case strings.HasPrefix(mimeType, "video/"):
		attrs = append(attrs, &tg.DocumentAttributeVideo{
			SupportsStreaming: true,
		})
	case strings.HasPrefix(mimeType, "audio/"):
		cleanTitle := strings.TrimSuffix(filename, filepath.Ext(filename))
		attrs = append(attrs, &tg.DocumentAttributeAudio{
			Title: cleanTitle,
		})
	}

	return attrs
}
