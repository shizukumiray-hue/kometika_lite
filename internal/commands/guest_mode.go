package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/message"
	htmlparser "github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"kometika-lite/internal/api"
	"kometika-lite/internal/api/facebook"
	"kometika-lite/internal/api/instagram"
	"kometika-lite/internal/api/tiktok"
	"kometika-lite/internal/api/twiter"
	"kometika-lite/internal/config"
	"kometika-lite/internal/log"
	"kometika-lite/internal/media"
)

const (
	maxGuestVideoSize = 60 * 1024 * 1024 // 60 MB
	maxGuestImageSize = 20 * 1024 * 1024 // 20 MB
)

var (
	cachePeer   tg.InputPeerClass
	cachePeerMu sync.Mutex
)

// InitGuestMode menyetel peer cache untuk upload media inline
func InitGuestMode(peer tg.InputPeerClass) {
	cachePeerMu.Lock()
	defer cachePeerMu.Unlock()
	cachePeer = peer
}

func getCachePeer() tg.InputPeerClass {
	cachePeerMu.Lock()
	defer cachePeerMu.Unlock()
	return cachePeer
}

// HandleGuestModeCommand menangani /guestmode atau /guest [on|off|status|toggle]
func HandleGuestModeCommand(ctx context.Context, client *tg.Client, msg *tg.Message, entities tg.Entities, args []string, user *tg.User, rootID int64, logger *zap.Logger) error {
	peer, _ := GetPeerFromMessage(ctx, client, msg, entities)
	if peer == nil {
		return nil
	}
	msgSender := message.NewSender(client).To(peer).Reply(msg.ID)

	if user == nil || user.ID != rootID {
		_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, "❌ <b>Akses Ditolak.</b>\nHanya Owner yang diizinkan mengelola Guest Mode."))
		return nil
	}

	fm := config.GetFeatureManager()
	subCmd := ""
	if len(args) > 0 {
		subCmd = strings.ToLower(strings.TrimSpace(args[0]))
	}

	switch subCmd {
	case "on", "enable", "1":
		fm.Enable("guest_mode")
		logger.Info("Guest mode diaktifkan oleh owner", zap.Int64("user_id", user.ID))
		_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, "✅ <b>Guest Mode</b> telah <u>diaktifkan</u>."))
	case "off", "disable", "0":
		fm.Disable("guest_mode")
		logger.Info("Guest mode dinonaktifkan oleh owner", zap.Int64("user_id", user.ID))
		_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, "❌ <b>Guest Mode</b> telah <u>dinonaktifkan</u>."))
	case "toggle":
		current := fm.IsEnabled("guest_mode")
		if current {
			fm.Disable("guest_mode")
			logger.Info("Guest mode ditoggle off oleh owner", zap.Int64("user_id", user.ID))
			_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, "❌ <b>Guest Mode</b> telah <u>dinonaktifkan</u>."))
		} else {
			fm.Enable("guest_mode")
			logger.Info("Guest mode ditoggle on oleh owner", zap.Int64("user_id", user.ID))
			_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, "✅ <b>Guest Mode</b> telah <u>diaktifkan</u>."))
		}
	case "status", "":
		statusText := "❌ <b>Nonaktif</b>"
		if fm.IsEnabled("guest_mode") {
			statusText = "✅ <b>Aktif</b>"
		}
		text := fmt.Sprintf("🛡️ <b>Status Guest Mode:</b> %s\n\n<b>Penggunaan:</b>\n• <code>/guestmode on</code> - Mengaktifkan fitur\n• <code>/guestmode off</code> - Menonaktifkan fitur\n• <code>/guestmode toggle</code> - Toggle status fitur\n• <code>/guestmode status</code> - Cek status fitur", statusText)
		_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, text))
	default:
		_, _ = msgSender.StyledText(ctx, htmlparser.String(nil, "⚠️ <b>Sub-command tidak dikenal.</b>\nGunakan: <code>/guestmode [on|off|toggle|status]</code>"))
	}

	return nil
}

// HandleBotGuestChatQuery menangani query Telegram UpdateBotGuestChatQuery
func HandleBotGuestChatQuery(ctx context.Context, client *tg.Client, upd *tg.UpdateBotGuestChatQuery, logger *zap.Logger) error {
	msg, ok := upd.Message.(*tg.Message)
	if !ok || msg.Message == "" {
		return nil
	}
	text := msg.Message

	var userID int64
	if msg.FromID != nil {
		if peer, ok := msg.FromID.(*tg.PeerUser); ok {
			userID = peer.UserID
		}
	}
	if userID == 0 && msg.PeerID != nil {
		if peer, ok := msg.PeerID.(*tg.PeerUser); ok {
			userID = peer.UserID
		}
	}

	queryID := upd.QueryID
	url := extractURLFromText(text)
	if url == "" {
		return nil
	}

	fm := config.GetFeatureManager()
	if !fm.IsEnabled("guest_mode") {
		logger.Info("Guest query ditolak: guest_mode dinonaktifkan", zap.Int64("user_id", userID))
		_, err := sendGuestInlineResult(ctx, client, queryID, "❌ Guest Mode saat ini sedang dinonaktifkan oleh admin.")
		return err
	}

	logger.Info("Guest query", zap.String("url", url), zap.Int64("user_id", userID))
	log.LogInfo(ctx, "📩 Guest query: "+url)

	inlineMsgID, err := sendGuestInlineResult(ctx, client, queryID, "⏳ Memproses URL, mohon tunggu...")
	if err != nil {
		logger.Error("Gagal kirim inline result", zap.Error(err))
		log.LogError(ctx, "GuestSendInline", err, "query_id="+fmt.Sprintf("%d", queryID))
		return err
	}

	safeGo("guest-task", func() { processGuestTask(ctx, client, inlineMsgID, url, logger) })
	return nil
}

func sendGuestInlineResult(ctx context.Context, client *tg.Client, queryID int64, text string) (tg.InputBotInlineMessageIDClass, error) {
	resultID := fmt.Sprintf("guest_%d", time.Now().UnixNano())
	result := &tg.InputBotInlineResult{
		ID:    resultID,
		Type:  "article",
		Title: text,
		SendMessage: &tg.InputBotInlineMessageText{
			Message: text,
		},
	}
	req := &tg.MessagesSetBotGuestChatResultRequest{
		QueryID: queryID,
		Result:  result,
	}
	return client.MessagesSetBotGuestChatResult(ctx, req)
}

func processGuestTask(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, url string, logger *zap.Logger) {
	var err error
	fm := config.GetFeatureManager()

	switch {
	case config.IsPlatformURL(url, "tiktok"):
		if !fm.IsEnabled("tiktok") {
			editGuestInlineText(ctx, client, inlineMsgID, "❌ Fitur TikTok sedang dinonaktifkan.")
			return
		}
		err = processGuestTikTok(ctx, client, inlineMsgID, url, logger)

	case config.IsPlatformURL(url, "instagram"):
		if !fm.IsEnabled("instagram") {
			editGuestInlineText(ctx, client, inlineMsgID, "❌ Fitur Instagram sedang dinonaktifkan.")
			return
		}
		err = processGuestInstagram(ctx, client, inlineMsgID, url, logger)

	case config.IsPlatformURL(url, "facebook"):
		if !fm.IsEnabled("facebook") {
			editGuestInlineText(ctx, client, inlineMsgID, "❌ Fitur Facebook sedang dinonaktifkan.")
			return
		}
		err = processGuestFacebook(ctx, client, inlineMsgID, url, logger)

	case config.IsPlatformURL(url, "twitter"):
		if !fm.IsEnabled("twitter") {
			editGuestInlineText(ctx, client, inlineMsgID, "❌ Fitur Twitter (X) sedang dinonaktifkan.")
			return
		}
		err = processGuestTwitter(ctx, client, inlineMsgID, url, logger)

	default:
		editGuestInlineText(ctx, client, inlineMsgID, "⚠️ Platform tidak didukung.\n\nGuest Mode saat ini hanya mendukung TikTok, Instagram, Facebook, dan Twitter (X).")
		log.LogWarn(ctx, "GuestUnsupported", "Platform tidak didukung", "url="+url)
		return
	}

	if err != nil {
		logger.Error("Guest download error", zap.Error(err))
		log.LogError(ctx, "GuestProcess", err, "url="+url)
		editGuestInlineText(ctx, client, inlineMsgID, fmt.Sprintf("❌ Gagal mengunduh: %v", err))
	}
}

// --- Platform Handlers ---

func processGuestTikTok(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, url string, logger *zap.Logger) error {
	editGuestInlineText(ctx, client, inlineMsgID, "🔍 Mendeteksi TikTok...")
	data, stream, err := tiktok.FetchTikTokDataWithFallback(ctx, logger, url, nil)
	if err != nil {
		return err
	}
	if stream != nil {
		defer stream.Close()
	}
	if data.VideoURL == "" {
		return fmt.Errorf("tidak ada video URL")
	}
	if data.IsAlbum && len(data.ImageURLs) > 1 {
		editGuestInlineText(ctx, client, inlineMsgID, "❌ Album tidak didukung di Guest Mode.")
		return nil
	}

	editGuestInlineText(ctx, client, inlineMsgID, "📥 Mengunduh video...")
	info := api.ContentTypeInfo{
		MimeType:  "video/mp4",
		Category:  api.ContentVideo,
		Extension: ".mp4",
	}
	filename := fmt.Sprintf("%s.mp4", data.ID)
	fileID, accessHash, fileRef, err := uploadMediaToCacheWithRetry(ctx, client, data.VideoURL, info, filename, data.CoverURL, 3)
	if err != nil {
		return err
	}
	caption := fmt.Sprintf("%s\n\n@Kometika_bot", data.Title)
	if len(caption) > 200 {
		caption = caption[:200]
	}
	inputMedia := &tg.InputMediaDocument{
		ID: &tg.InputDocument{
			ID:            fileID,
			AccessHash:    accessHash,
			FileReference: fileRef,
		},
	}
	return editGuestInlineMedia(ctx, client, inlineMsgID, inputMedia, caption)
}

func processGuestInstagram(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, url string, logger *zap.Logger) error {
	editGuestInlineText(ctx, client, inlineMsgID, "🔍 Mendeteksi Instagram...")
	data, err := instagram.FetchInstagramDataWithFallback(ctx, url)
	if err != nil {
		return err
	}
	if len(data.ImageURLs) > 1 {
		editGuestInlineText(ctx, client, inlineMsgID, "❌ Album tidak didukung di Guest Mode.")
		return nil
	}
	if data.VideoURL != "" {
		editGuestInlineText(ctx, client, inlineMsgID, "📥 Mengunduh video...")
		info := api.ContentTypeInfo{
			MimeType:  "video/mp4",
			Category:  api.ContentVideo,
			Extension: ".mp4",
		}
		filename := fmt.Sprintf("%s.mp4", data.ID)
		fileID, accessHash, fileRef, err := uploadMediaToCacheWithRetry(ctx, client, data.VideoURL, info, filename, data.CoverURL, 3)
		if err != nil {
			return err
		}
		caption := fmt.Sprintf("%s\n\n@Kometika_bot", data.Title)
		if len(caption) > 400 {
			caption = caption[:400]
		}
		inputMedia := &tg.InputMediaDocument{
			ID: &tg.InputDocument{
				ID:            fileID,
				AccessHash:    accessHash,
				FileReference: fileRef,
			},
		}
		return editGuestInlineMedia(ctx, client, inlineMsgID, inputMedia, caption)
	}
	if len(data.ImageURLs) == 1 {
		imgURL := data.ImageURLs[0]
		editGuestInlineText(ctx, client, inlineMsgID, "📥 Mengunduh foto...")
		fileID, accessHash, fileRef, err := uploadImageToCacheWithRetry(ctx, client, imgURL, 3)
		if err != nil {
			return err
		}
		caption := fmt.Sprintf("%s\n\n@Kometika_bot", data.Title)
		if len(caption) > 200 {
			caption = caption[:200]
		}
		inputMedia := &tg.InputMediaPhoto{
			ID: &tg.InputPhoto{
				ID:            fileID,
				AccessHash:    accessHash,
				FileReference: fileRef,
			},
		}
		return editGuestInlineMedia(ctx, client, inlineMsgID, inputMedia, caption)
	}
	return fmt.Errorf("tidak ada media")
}

func processGuestFacebook(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, url string, logger *zap.Logger) error {
	editGuestInlineText(ctx, client, inlineMsgID, "🔍 Mendeteksi Facebook...")
	data, err := facebook.FetchFacebookWithFallback(ctx, logger, url)
	if err != nil {
		return err
	}
	if data.VidioURL == "" {
		return fmt.Errorf("tidak ada video URL")
	}
	editGuestInlineText(ctx, client, inlineMsgID, "📥 Mengunduh video...")
	info := api.ContentTypeInfo{
		MimeType:  "video/mp4",
		Category:  api.ContentVideo,
		Extension: ".mp4",
	}
	filename := fmt.Sprintf("%s.mp4", data.ID)
	fileID, accessHash, fileRef, err := uploadMediaToCacheWithRetry(ctx, client, data.VidioURL, info, filename, data.CoverURL, 3)
	if err != nil {
		return err
	}
	caption := fmt.Sprintf("%s\n\n@Kometika_bot", data.Title)
	if len(caption) > 200 {
		caption = caption[:200]
	}
	inputMedia := &tg.InputMediaDocument{
		ID: &tg.InputDocument{
			ID:            fileID,
			AccessHash:    accessHash,
			FileReference: fileRef,
		},
	}
	return editGuestInlineMedia(ctx, client, inlineMsgID, inputMedia, caption)
}

func processGuestTwitter(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, url string, logger *zap.Logger) error {
	editGuestInlineText(ctx, client, inlineMsgID, "🔍 Mendeteksi Twitter...")
	data, err := twiter.FetchTwitterWithFallback(ctx, url)
	if err != nil {
		return err
	}
	if data.DownloadURL == "" {
		return fmt.Errorf("tidak ada media URL")
	}
	if data.IsAlbum && len(data.ImageURLs) > 1 {
		editGuestInlineText(ctx, client, inlineMsgID, "❌ Album tidak didukung di Guest Mode.")
		return nil
	}
	editGuestInlineText(ctx, client, inlineMsgID, "📥 Mengunduh media...")
	info := api.ContentTypeInfo{
		MimeType:  "video/mp4",
		Category:  api.ContentVideo,
		Extension: ".mp4",
	}
	filename := fmt.Sprintf("%s.mp4", data.ID)
	fileID, accessHash, fileRef, err := uploadMediaToCacheWithRetry(ctx, client, data.DownloadURL, info, filename, data.CoverURL, 3)
	if err != nil {
		fileID, accessHash, fileRef, err2 := uploadImageToCacheWithRetry(ctx, client, data.DownloadURL, 3)
		if err2 != nil {
			return err
		}
		caption := fmt.Sprintf("%s\n\n@Kometika_bot", data.Title)
		if len(caption) > 200 {
			caption = caption[:200]
		}
		inputMedia := &tg.InputMediaPhoto{
			ID: &tg.InputPhoto{
				ID:            fileID,
				AccessHash:    accessHash,
				FileReference: fileRef,
			},
		}
		return editGuestInlineMedia(ctx, client, inlineMsgID, inputMedia, caption)
	}
	caption := fmt.Sprintf("%s\n\n@Kometika_bot", data.Title)
	if len(caption) > 400 {
		caption = caption[:400]
	}
	inputMedia := &tg.InputMediaDocument{
		ID: &tg.InputDocument{
			ID:            fileID,
			AccessHash:    accessHash,
			FileReference: fileRef,
		},
	}
	return editGuestInlineMedia(ctx, client, inlineMsgID, inputMedia, caption)
}

// --- Upload Helper Functions ---

func uploadMediaToCacheWithRetry(ctx context.Context, client *tg.Client, mediaURL string, info api.ContentTypeInfo, filename, thumbURL string, maxRetries int) (int64, int64, []byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fileID, accessHash, fileRef, err := uploadMediaToCache(ctx, client, mediaURL, info, filename, thumbURL)
		if err == nil {
			return fileID, accessHash, fileRef, nil
		}
		lastErr = err

		retryDelay := time.Duration(attempt) * 500 * time.Millisecond
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return 0, 0, nil, ctx.Err()
		}
	}
	return 0, 0, nil, fmt.Errorf("setelah %d percobaan: %w", maxRetries, lastErr)
}

func uploadImageToCacheWithRetry(ctx context.Context, client *tg.Client, imageURL string, maxRetries int) (int64, int64, []byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fileID, accessHash, fileRef, err := uploadImageToCache(ctx, client, imageURL)
		if err == nil {
			return fileID, accessHash, fileRef, nil
		}
		lastErr = err

		retryDelay := time.Duration(attempt) * 500 * time.Millisecond
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return 0, 0, nil, ctx.Err()
		}
	}
	return 0, 0, nil, fmt.Errorf("setelah %d percobaan: %w", maxRetries, lastErr)
}

func uploadMediaToCache(ctx context.Context, client *tg.Client, mediaURL string, info api.ContentTypeInfo, filename, thumbURL string) (int64, int64, []byte, error) {
	stream, _, err := api.GetVideoStream(ctx, mediaURL)
	if err != nil {
		return 0, 0, nil, err
	}
	defer stream.Close()

	limitedStream := io.LimitReader(stream, maxGuestVideoSize)

	peer := getCachePeer()
	if peer == nil {
		var err2 error
		peer, err2 = getSelfPeer(ctx, client)
		if err2 != nil {
			return 0, 0, nil, err2
		}
	}

	up := uploader.NewUploader(client).WithThreads(1)
	file, err := up.FromReader(ctx, filename, limitedStream)
	if err != nil {
		return 0, 0, nil, err
	}

	var thumbFile tg.InputFileClass
	if thumbURL != "" {
		thumbBytes, err := api.GetThumbnail(ctx, thumbURL)
		if err == nil && len(thumbBytes) > 0 {
			thumbFile, _ = up.FromBytes(ctx, "thumb.jpg", thumbBytes)
		}
	}

	doc := &tg.InputMediaUploadedDocument{
		File:     file,
		MimeType: info.MimeType,
	}
	if thumbFile != nil {
		doc.SetThumb(thumbFile)
	}

	var attrs []tg.DocumentAttributeClass
	switch info.Category {
	case api.ContentVideo:
		videoAttr := &tg.DocumentAttributeVideo{}
		if info.MimeType == "video/mp4" {
			videoAttr.SupportsStreaming = true
		}
		attrs = append(attrs, videoAttr)
	case api.ContentAudio:
		attrs = append(attrs, &tg.DocumentAttributeAudio{})
	}
	attrs = append(attrs, &tg.DocumentAttributeFilename{FileName: filename})
	doc.Attributes = attrs

	randID, err := media.RandomID()
	if err != nil {
		return 0, 0, nil, err
	}

	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    doc,
		Message:  "temp",
		RandomID: randID,
	}
	updates, err := client.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, 0, nil, err
	}

	switch v := updates.(type) {
	case *tg.Updates:
		for _, update := range v.Updates {
			if msg, ok := update.(*tg.UpdateNewMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := docMedia.Document.(*tg.Document); ok {
							return doc.ID, doc.AccessHash, doc.FileReference, nil
						}
					}
				}
			}
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := docMedia.Document.(*tg.Document); ok {
							return doc.ID, doc.AccessHash, doc.FileReference, nil
						}
					}
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range v.Updates {
			if msg, ok := update.(*tg.UpdateNewMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := docMedia.Document.(*tg.Document); ok {
							return doc.ID, doc.AccessHash, doc.FileReference, nil
						}
					}
				}
			}
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := docMedia.Document.(*tg.Document); ok {
							return doc.ID, doc.AccessHash, doc.FileReference, nil
						}
					}
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		if docMedia, ok := v.Media.(*tg.MessageMediaDocument); ok {
			if doc, ok := docMedia.Document.(*tg.Document); ok {
				return doc.ID, doc.AccessHash, doc.FileReference, nil
			}
		}
	default:
		return 0, 0, nil, fmt.Errorf("tipe updates tidak didukung: %T", updates)
	}
	return 0, 0, nil, fmt.Errorf("tidak dapat menemukan document ID")
}

func uploadImageToCache(ctx context.Context, client *tg.Client, imageURL string) (int64, int64, []byte, error) {
	stream, _, err := api.GetVideoStream(ctx, imageURL)
	if err != nil {
		return 0, 0, nil, err
	}
	defer stream.Close()

	limitedStream := io.LimitReader(stream, maxGuestImageSize)
	body, err := io.ReadAll(limitedStream)
	if err != nil {
		return 0, 0, nil, err
	}

	convertedBody, err := media.ProcessAndValidateImageBytes(bytes.NewReader(body), nil)
	if err == nil && len(convertedBody) > 0 {
		body = convertedBody
	}

	peer := getCachePeer()
	if peer == nil {
		var err2 error
		peer, err2 = getSelfPeer(ctx, client)
		if err2 != nil {
			return 0, 0, nil, err2
		}
	}

	up := uploader.NewUploader(client).WithThreads(1)
	file, err := up.FromBytes(ctx, "temp.jpg", body)
	if err != nil {
		return 0, 0, nil, err
	}

	randID, err := media.RandomID()
	if err != nil {
		return 0, 0, nil, err
	}

	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    &tg.InputMediaUploadedPhoto{File: file},
		Message:  "temp",
		RandomID: randID,
	}
	updates, err := client.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, 0, nil, err
	}

	switch v := updates.(type) {
	case *tg.Updates:
		for _, update := range v.Updates {
			if msg, ok := update.(*tg.UpdateNewMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if photoMedia, ok := m.Media.(*tg.MessageMediaPhoto); ok {
						if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
							return photo.ID, photo.AccessHash, photo.FileReference, nil
						}
					}
				}
			}
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if photoMedia, ok := m.Media.(*tg.MessageMediaPhoto); ok {
						if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
							return photo.ID, photo.AccessHash, photo.FileReference, nil
						}
					}
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range v.Updates {
			if msg, ok := update.(*tg.UpdateNewMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if photoMedia, ok := m.Media.(*tg.MessageMediaPhoto); ok {
						if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
							return photo.ID, photo.AccessHash, photo.FileReference, nil
						}
					}
				}
			}
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					if photoMedia, ok := m.Media.(*tg.MessageMediaPhoto); ok {
						if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
							return photo.ID, photo.AccessHash, photo.FileReference, nil
						}
					}
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		if photoMedia, ok := v.Media.(*tg.MessageMediaPhoto); ok {
			if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
				return photo.ID, photo.AccessHash, photo.FileReference, nil
			}
		}
	default:
		return 0, 0, nil, fmt.Errorf("tipe updates tidak didukung: %T", updates)
	}
	return 0, 0, nil, fmt.Errorf("tidak dapat menemukan photo ID")
}

func getSelfPeer(ctx context.Context, client *tg.Client) (tg.InputPeerClass, error) {
	users, err := client.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil || len(users) == 0 {
		return nil, fmt.Errorf("cannot get self user")
	}
	u, ok := users[0].(*tg.User)
	if !ok {
		return nil, fmt.Errorf("self user not valid")
	}
	return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, nil
}

func editGuestInlineText(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, text string) {
	req := &tg.MessagesEditInlineBotMessageRequest{
		ID: inlineMsgID,
	}
	req.Message = text
	_, _ = client.MessagesEditInlineBotMessage(ctx, req)
}

func editGuestInlineMedia(ctx context.Context, client *tg.Client, inlineMsgID tg.InputBotInlineMessageIDClass, media tg.InputMediaClass, caption string) error {
	req := &tg.MessagesEditInlineBotMessageRequest{
		ID:    inlineMsgID,
		Media: media,
	}
	if caption != "" {
		req.Message = caption
	}
	_, err := client.MessagesEditInlineBotMessage(ctx, req)
	return err
}

func extractURLFromText(text string) string {
	words := strings.Fields(text)
	for _, w := range words {
		if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
			return w
		}
	}
	return ""
}
