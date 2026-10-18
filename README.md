# Kometika Lite - TeraBox & Multi-Platform Telegram Bot

Bot Telegram pengunduh media TeraBox dan multi-platform berbasis Go dengan Circuit Breaker berlapis dan Zero-Disk Streaming ke Telegram.

## Fitur
- **Multi-layer Fallback**: Komiktap API, Sechno API, Terabox Direct, Iteraplay, FlowVideoPlayer, Netlify API, dan Terabox Native.
- **Circuit Breaker**: Menggunakan `github.com/sony/gobreaker/v2`.
- **Zero-Disk Streaming**: Stream langsung via MTProto GOTD tanpa simpan file sementara di storage lokal (hemat disk).
- **Limit Proteksi**: Mendukung hingga 2 GB per file via Telegram MTProto.

## Cara Menjalankan Bot
1. Siapkan `.env` (lihat `.env.example`).
2. Jalankan bot:
   ```bash
   go run main.go
   ```
   Atau build binary:
   ```bash
   go build -o bot_lite main.go
   ./bot_lite
   ```
