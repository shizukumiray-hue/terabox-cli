# TeraBox CLI - Zero-Disk Telegram Streamer

Tool CLI mandiri untuk mengunduh dan mengirim berkas dari TeraBox langsung ke Telegram pengguna via MTProto dengan pendekatan **Zero-Disk Streaming** (tanpa menyimpan file sementara di harddisk lokal).

## Fitur Utama

- **Interactive CLI Loop (REPL)**: Sesi interaktif berkelanjutan (`terabox> `) yang siap menerima input link berulang kali tanpa harus keluar dari aplikasi.
- **High-Speed `fasthttp` Streaming**: Menggunakan engine HTTP berkecepatan tinggi `valyala/fasthttp` dengan connection pooling untuk streaming download zero-disk ke Telegram MTProto.
- **Auto-Retry & Exponential Backoff**: Menangani kegagalan unduh akibat HTTP 5xx (500, 502, 503, 504), network glitch, dan timeout secara otomatis dengan 3 kali percobaan bertahap.
- **TUI & CLI Beautification**: Banner ASCII art keren, pewarnaan ANSI, format tabel berkas yang rapi, dan indikator progress transfer lengkap dengan kecepatan streaming.
- **Multi-layer Fallback & Scraper**: Terintegrasi dengan 6 provider scraper:
  1. Sechno API
  2. Terabox Direct (Mayumi PC API)
  3. Iteraplay
  4. FlowVideoPlayer
  5. Netlify API
  6. Terabox Native Share List
- **Circuit Breaker Resiliency**: Dilindungi dengan `gobreaker/v2` untuk auto-trip & fallback jika ada API scraper yang down atau cooldown.
- **File Size Support**: Mendukung hingga batas Telegram Bot (2 GB per file).
- **Interactive File Selection**: Memilih file tertentu (misal: `1`, `1,3`, `1-5`, atau `all`).

## Persyaratan

- Go 1.21+ (Disarankan Go 1.26+)
- Akun Telegram (API ID & API Hash dari [my.telegram.org](https://my.telegram.org))
- Bot Token dari [@BotFather](https://t.me/BotFather)

## Konfigurasi

1. Salin `.env.example` menjadi `.env`:
   ```bash
   cp .env.example .env
   ```
2. Isi variabel environment:
   ```ini
   TELEGRAM_API_ID=12345678
   TELEGRAM_API_HASH=abcdef1234567890abcdef1234567890
   TELEGRAM_BOT_TOKEN=1234567890:ABCdef...
   TARGET_USER_ID=123456789
   ```
   > **Catatan**: Pastikan akun `TARGET_USER_ID` sudah pernah mengirim `/start` ke bot Anda di Telegram agar bot memiliki izin untuk mengirim berkas.

## Kompilasi & Build

Kompilasi binary mandiri:
```bash
go build -o terabox-sender main.go
```

## Cara Penggunaan

### 1. Mode Interaktif (REPL)
Jalankan binary tanpa argumen:
```bash
./terabox-sender
```
Aplikasi akan masuk ke prompt interaktif `terabox> `:
- `url <link>` atau langsung masukkan link: proses link TeraBox dan pilih berkas untuk dikirim.
- `clear`: membersihkan tampilan terminal.
- `help`: menampilkan daftar perintah.
- `exit` / `quit`: keluar dari aplikasi secara bersih.

Setelah memproses satu tautan, bot akan kembali ke prompt `terabox> ` dan siap menerima tautan berikutnya tanpa restart aplikasi.

### 2. Mode Argumen CLI Langsung
Masukkan link TeraBox sebagai argumen:
```bash
./terabox-sender "https://terabox.com/s/1xxxxxxxxx"
```
Setelah link argumen selesai diproses, bot otomatis melanjutkan ke mode interaktif REPL.

### 3. Memilih File
Setelah daftar berkas ditemukan, masukkan pilihan:
- `1` : Hanya unduh berkas no 1.
- `1,3,5` : Unduh berkas 1, 3, dan 5.
- `1-4` : Unduh berkas 1 sampai 4.
- `all` atau `semua` : Unduh semua berkas.
- `c` : Batalkan pilihan berkas.
