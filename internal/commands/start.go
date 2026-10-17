package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func HandleStart(ctx context.Context, tgClient *tg.Client, msg *tg.Message, entities tg.Entities, user *tg.User, logger *zap.Logger) error {
	peer, err := GetPeerFromMessage(ctx, tgClient, msg, entities)
	if err != nil {
		logger.Error("Gagal mendapatkan peer saat HandleStart", zap.Error(err))
		return err
	}

	var fullName string
	if user != nil {
		fullName = strings.TrimSpace(fmt.Sprintf("%s %s", user.FirstName, user.LastName))
	}
	if fullName == "" {
		fullName = "Pengguna"
	}

	htmlMsg := fmt.Sprintf(
		`Selamat datang, <b>%s</b>! 👋

Saya adalah <b>Kometika Lite Bot</b> (100%% Native Go Downloader).

🌐 <b>Platform yang Didukung:</b>
• TikTok (Video, Slide Foto, Audio MP3)
• Instagram (Reels, Post/Carousel, Story)
• Facebook (Video HD/SD, Reels)
• Twitter / X (Video, Foto)
• Terabox (Direct Download)
• MediaFire (Direct Download)

📌 <b>Cara Penggunaan:</b>
• <i>Chat Privat:</i> Cukup kirim URL secara langsung.
• <i>Grup / Forum:</i> Gunakan perintah /dl diikuti URL.
  Format: <code>.dl [URL]</code>

⚡ <b>Prefix yang Tersedia:</b>
<code>.</code> <code>!</code> <code>/</code>

💡 <b>Contoh:</b>
<code>.dl https://www.tiktok.com/@username/video/1234567890</code>`,
		fullName,
	)

	sender := message.NewSender(tgClient)
	_, err = sender.To(peer).Reply(msg.ID).StyledText(ctx, html.String(nil, htmlMsg))
	if err != nil {
		logger.Warn("Gagal mengirim pesan HTML, fallback ke plain text", zap.Error(err))
		plainMsg := fmt.Sprintf(
			"Selamat datang, %s!\n\nKometika Lite Bot siap membantu download dari TikTok, Instagram, Facebook, Twitter, Terabox, dan MediaFire.\n\nKirim URL langsung di private chat atau gunakan .dl <url> di grup.",
			fullName,
		)
		_, err = sender.To(peer).Reply(msg.ID).Text(ctx, plainMsg)
	}

	return err
}
