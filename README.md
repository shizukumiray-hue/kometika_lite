# Kometika Lite - TeraBox Telegram Bot & CLI

Bot Telegram dan CLI pengunduh media TeraBox berbasis Go dengan Circuit Breaker berlapis dan Zero-Disk Streaming ke Telegram.

## Fitur
- **Multi-layer Fallback**: Sechno API, Terabox Direct, Iteraplay, FlowVideoPlayer, Netlify API, dan Terabox Native.
- **Circuit Breaker**: Menggunakan `github.com/sony/gobreaker/v2`.
- **Zero-Disk Streaming**: Stream langsung via MTProto GOTD tanpa simpan file sementara di storage lokal (hemat disk).
- **Limit Proteksi**: Mendukung hingga 2 GB per file via Telegram MTProto.

## Cara Pakai CLI
1. Siapkan `.env` (lihat `.env.example`).
2. Jalankan:
   ```bash
   go run cmd/terabox/main.go
   ```
   Atau dengan binary:
   ```bash
   ./terabox-sender "https://1024terabox.com/s/xxxx"
   ```
