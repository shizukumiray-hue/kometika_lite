package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"
	"github.com/showwin/speedtest-go/speedtest"
	"go.uber.org/zap"

	"kometika-lite/internal/media"
)

const speedTestTimeout = 60 * time.Second

type SpeedTestResult struct {
	ServerName    string
	ServerCountry string
	Latency       time.Duration
	DownloadMbps  float64
	UploadMbps    float64
}

func runSpeedTestInternal() (*SpeedTestResult, error) {
	client := speedtest.New()

	serverList, err := client.FetchServers()
	if err != nil {
		return nil, fmt.Errorf("fetch server list: %w", err)
	}

	targets, err := serverList.FindServer([]int{})
	if err != nil || len(targets) == 0 {
		return nil, fmt.Errorf("tidak ada server yang ditemukan: %w", err)
	}
	s := targets[0]

	if err := s.PingTest(nil); err != nil {
		return nil, fmt.Errorf("ping test gagal: %w", err)
	}

	if err := s.DownloadTest(); err != nil {
		return nil, fmt.Errorf("download test gagal: %w", err)
	}

	if err := s.UploadTest(); err != nil {
		return nil, fmt.Errorf("upload test gagal: %w", err)
	}

	dlMbps := float64(s.DLSpeed) * 8 / 1_000_000
	ulMbps := float64(s.ULSpeed) * 8 / 1_000_000

	return &SpeedTestResult{
		ServerName:    fmt.Sprintf("%s (%s)", s.Name, s.Host),
		ServerCountry: s.Country,
		Latency:       s.Latency,
		DownloadMbps:  dlMbps,
		UploadMbps:    ulMbps,
	}, nil
}

func runSpeedTest() (*SpeedTestResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), speedTestTimeout)
	defer cancel()

	resultCh := make(chan *SpeedTestResult, 1)
	errCh := make(chan error, 1)

	safeGo("speedtest-run", func() {
		result, err := runSpeedTestInternal()
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	})

	select {
	case res := <-resultCh:
		return res, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("speedtest timeout setelah %v", speedTestTimeout)
	}
}

func formatSpeedTestTelegram(result *SpeedTestResult) string {
	var sb strings.Builder

	sb.WriteString("🌐 <b>SPEEDTEST RESULT</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")

	sb.WriteString("📡 <b>Server</b>\n")
	sb.WriteString(fmt.Sprintf("└ <code>%s</code>\n", result.ServerName))
	if result.ServerCountry != "" {
		sb.WriteString(fmt.Sprintf("   🌍 <code>%s</code>\n", result.ServerCountry))
	}
	sb.WriteString("\n")

	sb.WriteString("⏱️ <b>Latency</b>\n")
	sb.WriteString(fmt.Sprintf("└ <code>%s</code>\n\n", result.Latency))

	sb.WriteString("📥 <b>Download</b>\n")
	sb.WriteString(fmt.Sprintf("└ <code>%.2f Mbps</code>\n\n", result.DownloadMbps))

	sb.WriteString("📤 <b>Upload</b>\n")
	sb.WriteString(fmt.Sprintf("└ <code>%.2f Mbps</code>\n\n", result.UploadMbps))

	return sb.String()
}

func HandleSpeedtest(ctx context.Context, client *tg.Client, msg *tg.Message, entities tg.Entities, logger *zap.Logger) error {
	peer, err := GetPeerFromMessage(ctx, client, msg, entities)
	if err != nil || peer == nil {
		logger.Error("Gagal mendapatkan peer di HandleSpeedtest", zap.Error(err))
		return err
	}

	sender := message.NewSender(client)

	loadingMsg, err := sender.To(peer).Reply(msg.ID).StyledText(ctx, html.String(nil, "⏳ <b>Menjalankan Speedtest...</b>\n<i>Mohon tunggu, proses ini memakan waktu sekitar 30-60 detik.</i>"))
	if err != nil {
		logger.Warn("Gagal mengirim pesan loading speedtest", zap.Error(err))
		return err
	}

	var loadingMsgID int
	if updates, ok := loadingMsg.(*tg.UpdateShortSentMessage); ok {
		loadingMsgID = updates.ID
	} else {
		loadingMsgID, _ = media.ExtractMessageID(loadingMsg)
	}

	result, err := runSpeedTest()
	if err != nil {
		logger.Error("Speedtest error", zap.Error(err))
		if loadingMsgID != 0 {
			_ = media.EditHTML(ctx, client, peer, loadingMsgID, "❌ <b>Gagal melakukan Speedtest.</b>\nCoba beberapa saat lagi.")
		}
		return err
	}

	finalHTML := formatSpeedTestTelegram(result)

	if loadingMsgID != 0 {
		err = media.EditHTML(ctx, client, peer, loadingMsgID, finalHTML)
		if err != nil {
			logger.Error("Gagal edit pesan hasil speedtest", zap.Error(err))
			_, _ = sender.To(peer).Reply(msg.ID).StyledText(ctx, html.String(nil, finalHTML))
		}
	}

	logger.Info("Speedtest berhasil diselesaikan", zap.Float64("download_mbps", result.DownloadMbps))
	return nil
}
