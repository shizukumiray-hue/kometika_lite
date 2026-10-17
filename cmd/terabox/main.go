package main

import (
	"bufio"
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/joho/godotenv"

	"kometika-lite/internal/api/terabox"
)

const maxTelegramFileSize = 2000 * 1024 * 1024 // 2 GB

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
		fmt.Fprintln(os.Stderr, "❌ Error: Variabel TELEGRAM_API_ID, TELEGRAM_API_HASH, TELEGRAM_BOT_TOKEN, dan TARGET_USER_ID (atau OWNER_ID) harus ada di .env")
		os.Exit(1)
	}

	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: TELEGRAM_API_ID tidak valid: %v\n", err)
		os.Exit(1)
	}

	targetUserID, err := strconv.ParseInt(targetIDStr, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: ID Telegram target tidak valid: %v\n", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	// 2. Input URL Terabox
	var inputURL string
	if len(os.Args) > 1 {
		inputURL = strings.TrimSpace(os.Args[1])
	} else {
		fmt.Print("🔗 Masukkan URL TeraBox: ")
		line, _ := reader.ReadString('\n')
		inputURL = strings.TrimSpace(line)
	}

	if inputURL == "" {
		fmt.Println("❌ URL tidak boleh kosong.")
		os.Exit(1)
	}

	// 3. Scrape dengan sistem multi-layer Circuit Breaker
	fmt.Printf("\n[*] Mengekstrak media via Circuit Breaker (Sechno/Mayumi/Iteraplay/Flow/Netlify/Native)...\n")
	files, err := terabox.FetchTeraboxUniversal(inputURL)
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "❌ Gagal mengambil data Terabox: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Berhasil! Ditemukan %d berkas:\n", len(files))
	fmt.Println(strings.Repeat("=", 86))
	fmt.Printf("%-4s | %-42s | %-12s | %s\n", "No", "Nama Berkas", "Ukuran", "Link Langsung")
	fmt.Println(strings.Repeat("-", 86))

	for i, f := range files {
		hasLink := "Tersedia"
		if f.DownloadURL == "" && f.StreamURL == "" {
			hasLink = "Perlu Akun"
		}
		name := f.FileName
		if len(name) > 42 {
			name = name[:39] + "..."
		}
		fmt.Printf("%-4d | %-42s | %-12s | %s\n", i+1, name, f.FileSize, hasLink)
	}
	fmt.Println(strings.Repeat("=", 86))

	// 4. Pilih media yang ingin diunduh
	fmt.Print("\n📥 Pilih berkas (contoh: 1 atau 1,3,5 atau 1-5 atau 'all' / 'semua'): ")
	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	selectedIndices := parseSelection(choiceStr, len(files))
	if len(selectedIndices) == 0 {
		fmt.Println("❌ Pilihan tidak valid atau dibatalkan.")
		os.Exit(0)
	}

	fmt.Printf("\n🚀 Memulai pengiriman %d berkas ke Telegram (Target User ID: %d) via Zero-Disk Streaming...\n", len(selectedIndices), targetUserID)

	// 5. Inisialisasi GOTD Bot Client
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sessPath := "session_terabox.json"
	if _, err := os.Stat("session.json"); err == nil {
		sessPath = "session.json"
	}

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
			fmt.Println("[*] Mengautentikasi bot token ke Telegram...")
			for {
				_, err := client.Auth().Bot(ctx, botToken)
				if err == nil {
					break
				}
				if d, ok := tgerr.AsFloodWait(err); ok {
					fmt.Printf("[!] Flood wait auth, menunggu %v...\n", d)
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
		httpClient := &http.Client{Timeout: 30 * time.Minute}

		// 6. Kirim satu per satu secara langsung (Zero Disk)
		for count, idx := range selectedIndices {
			fileTarget := files[idx]
			dlURL := fileTarget.DownloadURL
			if dlURL == "" {
				dlURL = fileTarget.StreamURL
			}

			if dlURL == "" {
				fmt.Printf("[%d/%d] ⚠️ Skip %s: Direct link tidak tersedia.\n", count+1, len(selectedIndices), fileTarget.FileName)
				continue
			}

			fmt.Printf("\n[%d/%d] 🌐 Menghubungkan stream: %s\n", count+1, len(selectedIndices), fileTarget.FileName)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
			if err != nil {
				fmt.Printf("[-] Gagal membuat request: %v\n", err)
				continue
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

			resp, err := httpClient.Do(req)
			if err != nil {
				fmt.Printf("[-] Gagal stream dari URL: %v\n", err)
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				resp.Body.Close()
				fmt.Printf("[-] Server mengembalikan HTTP %d\n", resp.StatusCode)
				continue
			}

			totalSize := resp.ContentLength
			// Validasi batas Telegram 2 GB
			if totalSize > maxTelegramFileSize {
				resp.Body.Close()
				fmt.Printf("[-] ⚠️ File melebihi batas 2 GB (%d bytes). Lewati upload.\n", totalSize)
				continue
			}

			// Bungkus reader untuk memantau progress stream tanpa menulis ke disk
			pr := &progressReader{
				reader: resp.Body,
				total:  totalSize,
				name:   fileTarget.FileName,
			}

			fmt.Printf("[*] Mengunggah langsung ke Telegram (Zero Disk)...\n")
			uploadFile, err := up.Upload(ctx, uploader.NewUpload(fileTarget.FileName, pr, totalSize))
			resp.Body.Close()

			if err != nil {
				fmt.Printf("\n[-] Gagal mengunggah ke Telegram: %v\n", err)
				continue
			}
			fmt.Printf("\n[+] Berhasil diunggah ke server Telegram. Mengirim pesan...\n")

			// Deteksi MimeType
			mimeType := mime.TypeByExtension(filepath.Ext(fileTarget.FileName))
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			caption := fmt.Sprintf("📁 <b>%s</b>\n⚖️ Ukuran: %s", fileTarget.FileName, fileTarget.FileSize)

			docReq := &tg.MessagesSendMediaRequest{
				Peer: peer,
				Media: &tg.InputMediaUploadedDocument{
					File:     uploadFile,
					MimeType: mimeType,
					Attributes: []tg.DocumentAttributeClass{
						&tg.DocumentAttributeFilename{FileName: fileTarget.FileName},
					},
				},
				Message:  caption,
				RandomID: time.Now().UnixNano(),
			}

			for {
				_, err = client.API().MessagesSendMedia(ctx, docReq)
				if err == nil {
					fmt.Printf("[✓] File terkirim ke user %d!\n", ownerID)
					break
				}
				if d, ok := tgerr.AsFloodWait(err); ok {
					fmt.Printf("[!] Flood wait kirim pesan, istirahat %v...\n", d)
					time.Sleep(d + time.Second)
					continue
				}
				fmt.Printf("[-] Gagal kirim media ke chat: %v\n", err)
				break
			}
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error eksekusi bot Telegram: %v\n", err)
	} else {
		fmt.Println("\n🎉 Selesai! Semua media terpilih berhasil diproses.")
	}
}

// progressReader memantau aliran byte langsung dari HTTP Response ke Telegram tanpa disk
type progressReader struct {
	reader io.Reader
	total  int64
	read   int64
	name   string
	lastP  int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		if pr.total > 0 {
			pct := int(float64(pr.read) / float64(pr.total) * 100)
			if pct != pr.lastP {
				pr.lastP = pct
				fmt.Printf("\r    --> Streaming ke Telegram: %s / %s (%d%%)",
					formatByteSize(pr.read), formatByteSize(pr.total), pct)
			}
		} else {
			fmt.Printf("\r    --> Streaming ke Telegram: %s", formatByteSize(pr.read))
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
