package commands

import (
	"context"
	"os"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/markup"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"kometika-lite/internal/media"
)

func HandleHelpCommand(ctx context.Context, client *tg.Client, msg *tg.Message, entities tg.Entities, user *tg.User, logger *zap.Logger) error {
	peer, err := GetPeerFromMessage(ctx, client, msg, entities)
	if err != nil || peer == nil {
		logger.Error("Gagal dapatkan peer untuk command help", zap.Error(err))
		return err
	}

	ownerID := os.Getenv("OWNER_ID")
	adminURL := "tg://user?id=" + ownerID

	htmlMsg := `<b>🛠 Panduan Penggunaan Kometika Lite Bot</b>

<b>🚀 Platform yang Didukung:</b>
• <b>TikTok</b> (Video No-Watermark, Album Foto, Audio)
• <b>Instagram</b> (Reels, Post Carousel, Story)
• <b>Facebook</b> (Video/Reels SD & HD)
• <b>Twitter / X</b> (Video, Foto)
• <b>YouTube Music</b> (Audio / Music MP3/M4A via <code>/music</code>)
• <b>Terabox</b> (Direct Link Video/File)
• <b>MediaFire</b> (Direct File Download)

<b>⌨️ Perintah yang Tersedia:</b>
• <code>/dl [url]</code> - Unduh link media di grup
• <code>/music [url]</code> - Unduh musik/audio YouTube
• <code>/ping</code> - Cek respons bot
• <code>/uptime</code> - Cek status monitoring server (Owner only)
• <code>/goroutine</code> - Cek status runtime go (DM only)
• <code>/getid</code> - Cek ID user / grup / channel (Owner only)
• <code>/fiture</code> atau <code>/status</code> - Cek status toggle fitur
• <code>/on [fitur]</code> / <code>/off [fitur]</code> - Aktifkan/Nonaktifkan fitur (Owner only)
• <code>/guestmode [on|off|toggle|status]</code> - Pengaturan Guest Mode (Owner only)
• <code>/getlog</code> - Unduh log server bot (Owner DM only)

💡 <i><b>Catatan:</b> Di Private Chat, cukup kirim link secara langsung.</i>
`

	replyMarkup := markup.InlineKeyboard(
		markup.Row(
			markup.URL("💬 Hubungi Admin", adminURL, markup.StyleBgPrimary()),
		),
	)

	sender := message.NewSender(client)
	sentMsg, err := sender.To(peer).Reply(msg.ID).Markup(replyMarkup).StyledText(ctx, html.String(nil, htmlMsg))
	if err != nil {
		logger.Error("Gagal mengirim pesan help", zap.Error(err))
		return err
	}

	msgID, errExt := media.ExtractMessageID(sentMsg)
	if errExt == nil && msgID != 0 {
		safeGo("help-cleanup", func() { scheduleHelpButtonCleanup(client, peer, msgID, logger) })
	}

	return nil
}

func scheduleHelpButtonCleanup(client *tg.Client, peer tg.InputPeerClass, msgID int, logger *zap.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	select {
	case <-time.After(5 * time.Minute):
		editCtx, editCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer editCancel()

		for {
			_, err := client.MessagesEditMessage(editCtx, &tg.MessagesEditMessageRequest{
				Peer:        peer,
				ID:          msgID,
				ReplyMarkup: &tg.ReplyKeyboardHide{},
			})

			if err != nil {
				if d, ok := tgerr.AsFloodWait(err); ok {
					logger.Warn("FloodWait saat hapus tombol help", zap.Duration("wait", d))
					select {
					case <-time.After(d + time.Second):
						continue
					case <-editCtx.Done():
						logger.Warn("Gagal menghapus tombol help (timeout)")
						return
					}
				}
				logger.Warn("Gagal menghapus tombol help", zap.Error(err))
				return
			}
			logger.Info("Tombol menu help dihapus otomatis", zap.Int("msg_id", msgID))
			break
		}
	case <-ctx.Done():
		return
	}
}
