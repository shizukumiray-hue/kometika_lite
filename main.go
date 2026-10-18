package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kometika-lite/internal/api"
	"kometika-lite/internal/cache"
	"kometika-lite/internal/commands"
	"kometika-lite/internal/config"
	"kometika-lite/internal/db"
	"kometika-lite/internal/log"
	"kometika-lite/internal/media"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger   *zap.Logger
	jobQueue chan func()
)

const (
	maxWorkers   = 200
	jobQueueSize = 1000
)

func initLogger() {
	file := newRotatingFile("mybot.log", 50<<20)
	writeSyncer := zapcore.AddSync(file)

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "time"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoder := zapcore.NewJSONEncoder(encoderCfg)

	core := zapcore.NewCore(encoder, writeSyncer, zapcore.DebugLevel)
	logger = zap.New(core)
	zap.ReplaceGlobals(logger)
}

func cleanupTmpFiles() {
	dir := os.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-6 * time.Hour)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "terabox_") &&
			!strings.HasPrefix(name, "TeraboxBatch_") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if e.IsDir() {
			if err := os.RemoveAll(filepath.Join(dir, name)); err == nil {
				logger.Info("Direktori sementara lama dibersihkan", zap.String("name", name))
			}
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			logger.Info("File sementara lama dibersihkan", zap.String("name", name))
		}
	}
}

type rotatingFile struct {
	*os.File
	path string
	max  int64
	size int64
}

func newRotatingFile(path string, maxBytes int64) *rotatingFile {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		panic(err)
	}
	fi, err := f.Stat()
	var size int64
	if err == nil {
		size = fi.Size()
	}
	return &rotatingFile{File: f, path: path, max: maxBytes, size: size}
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	if r.size+int64(len(p)) > r.max {
		r.rotate()
	}
	n, err := r.File.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() {
	_ = r.File.Sync()
	_ = r.File.Close()
	_ = os.Remove(r.path + ".1")
	_ = os.Rename(r.path, r.path+".1")
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		panic(err)
	}
	r.File = f
	r.size = 0
}

func recoverLog(ctx context.Context, name string) {
	if r := recover(); r != nil {
		logger.Error("Panic di handler "+name,
			zap.Any("recover", r),
			zap.Stack("stack"),
		)
	}
}

func startWorkerPool(ctx context.Context, wg *sync.WaitGroup) {
	for i := range maxWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case job := <-jobQueue:
					if job == nil {
						return
					}
					func() {
						defer func() {
							if r := recover(); r != nil {
								logger.Error("Worker panic", zap.Int("worker", workerID), zap.Any("recover", r))
							}
						}()
						job()
					}()
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}
	logger.Info("Worker pool started", zap.Int("workers", maxWorkers), zap.Int("queue_size", jobQueueSize))
}

func enqueueJob(job func()) {
	select {
	case jobQueue <- job:
	default:
		logger.Warn("Job queue full, dropping task")
	}
}

func getUserIDFromMsg(msg *tg.Message) int64 {
	if peer, ok := msg.PeerID.(*tg.PeerUser); ok {
		return peer.UserID
	}
	return 0
}

func handleAutoDownload(ctx context.Context, text string, tgClient *tg.Client, msg *tg.Message, entities tg.Entities) {
	_ = ctx
	switch {
	case config.IsPlatformURL(text, "tiktok"):
		enqueueJob(func() {
			bgCtx := context.Background()
			if err := commands.HandleTikTok(bgCtx, tgClient, msg, entities, text, logger); err != nil {
				logger.Error("Auto TikTok error", zap.Error(err))
				log.LogError(bgCtx, "HandleTikTok", err,
					fmt.Sprintf("URL: %s", text),
					fmt.Sprintf("UserID: %d", getUserIDFromMsg(msg)),
				)
			}
		})
	case config.IsPlatformURL(text, "instagram"):
		enqueueJob(func() {
			bgCtx := context.Background()
			if err := commands.HandleInstagram(bgCtx, tgClient, msg, entities, text, logger); err != nil {
				logger.Error("Auto Instagram error", zap.Error(err))
				log.LogError(bgCtx, "HandleInstagram", err,
					fmt.Sprintf("URL: %s", text),
					fmt.Sprintf("UserID: %d", getUserIDFromMsg(msg)),
				)
			}
		})
	case config.IsPlatformURL(text, "facebook"):
		enqueueJob(func() {
			bgCtx := context.Background()
			if err := commands.HandleFacebook(bgCtx, tgClient, msg, entities, text, logger); err != nil {
				logger.Error("Auto Facebook error", zap.Error(err))
				log.LogError(bgCtx, "HandleFacebook", err,
					fmt.Sprintf("URL: %s", text),
					fmt.Sprintf("UserID: %d", getUserIDFromMsg(msg)),
				)
			}
		})
	case config.IsPlatformURL(text, "twitter"):
		enqueueJob(func() {
			bgCtx := context.Background()
			if err := commands.HandleTwitter(bgCtx, tgClient, msg, entities, text, logger); err != nil {
				logger.Error("Auto Twitter error", zap.Error(err))
				log.LogError(bgCtx, "HandleTwitter", err,
					fmt.Sprintf("URL: %s", text),
					fmt.Sprintf("UserID: %d", getUserIDFromMsg(msg)),
				)
			}
		})
	case config.IsTeraboxLink(text):
		enqueueJob(func() {
			bgCtx := context.Background()
			if err := commands.HandleTerabox(bgCtx, tgClient, msg, entities, text); err != nil {
				logger.Error("Auto Terabox error", zap.Error(err))
				log.LogError(bgCtx, "HandleTerabox", err,
					fmt.Sprintf("URL: %s", text),
					fmt.Sprintf("UserID: %d", getUserIDFromMsg(msg)),
				)
			}
		})
	case config.IsPlatformURL(text, "mediafire"):
		enqueueJob(func() {
			bgCtx := context.Background()
			if err := commands.HandleMediaFire(bgCtx, tgClient, msg, entities, text, logger); err != nil {
				logger.Error("Auto MediaFire error", zap.Error(err))
				log.LogError(bgCtx, "HandleMediaFire", err,
					fmt.Sprintf("URL: %s", text),
					fmt.Sprintf("UserID: %d", getUserIDFromMsg(msg)),
				)
			}
		})
	}
}

func getUserFromMessage(ctx context.Context, tgClient *tg.Client, msg *tg.Message, entities tg.Entities) *tg.User {
	if msg.FromID != nil {
		switch peer := msg.FromID.(type) {
		case *tg.PeerUser:
			if u, exists := entities.Users[peer.UserID]; exists {
				return u
			}
			users, err := tgClient.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: peer.UserID}})
			if err == nil && len(users) > 0 {
				if u, ok := users[0].(*tg.User); ok {
					return u
				}
			}
			return &tg.User{ID: peer.UserID}
		}
	}

	switch peer := msg.PeerID.(type) {
	case *tg.PeerUser:
		if u, exists := entities.Users[peer.UserID]; exists {
			return u
		}
		users, err := tgClient.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: peer.UserID}})
		if err == nil && len(users) > 0 {
			if u, ok := users[0].(*tg.User); ok {
				return u
			}
		}
		return &tg.User{ID: peer.UserID}
	}
	return nil
}

func logUserAction(user *tg.User, action, text string) {
	if user == nil || action == "" {
		return
	}
	username := user.Username
	if username == "" {
		username = user.FirstName
	}
	logger.Info("User action",
		zap.String("username", "@"+username),
		zap.Int64("user_id", user.ID),
		zap.String("action", action),
		zap.String("message", text),
	)
}

func handleCommand(ctx context.Context, tgClient *tg.Client, msg *tg.Message, entities tg.Entities, user *tg.User, rootID int64, botUsername string, text string) error {
	var cmd string
	var args []string
	found := false

	for _, prefix := range []string{"/", ".", "!"} {
		if rest, ok := strings.CutPrefix(text, prefix); ok {
			parts := strings.Fields(rest)
			if len(parts) > 0 {
				rawCmd := strings.ToLower(parts[0])
				cmdParts := strings.SplitN(rawCmd, "@", 2)
				cmd = cmdParts[0]

				if len(cmdParts) > 1 {
					targetUsername := cmdParts[1]
					expectedUsername := strings.ToLower(strings.TrimPrefix(botUsername, "@"))
					if targetUsername != expectedUsername {
						return nil
					}
				}

				args = parts[1:]
				found = true
				break
			}
		}
	}

	if !found {
		return nil
	}

	logUserAction(user, cmd, text)

	router := commands.NewCommandRouter(tgClient, rootID, botUsername, logger)

	needsAsync := []string{"dl", "start", "fiture", "features", "liststatus", "status", "on", "off", "help", "getlog", "guestmode", "guest", "speedtest", "testspeed", "uptime"}
	for _, asyncCmd := range needsAsync {
		if cmd == asyncCmd {
			enqueueJob(func() {
				bgCtx := context.Background()
				if err := router.RouteCommand(bgCtx, msg, entities, user, cmd, args); err != nil {
					logger.Error("Command error", zap.String("cmd", cmd), zap.Error(err))
				}
			})
			return nil
		}
	}

	return router.RouteCommand(ctx, msg, entities, user, cmd, args)
}

func loadConfig() {
	_ = godotenv.Load()

	// Fallback baca config.json jika .env tidak ada atau variabel kosong
	jsonFiles := []string{"config.json", "setting.json", "settings.json"}
	for _, f := range jsonFiles {
		if data, err := os.ReadFile(f); err == nil {
			var conf map[string]interface{}
			if err := json.Unmarshal(data, &conf); err == nil {
				for k, v := range conf {
					envKey := strings.ToUpper(k)
					if os.Getenv(envKey) == "" {
						os.Setenv(envKey, fmt.Sprintf("%v", v))
					}
				}
				break
			}
		}
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	loadConfig()

	initLogger()
	cleanupTmpFiles()
	defer logger.Sync()
	db.InitDB("kometika_bot.db")
	defer db.DB.Close()

	cache.StartJanitor()

	fm := config.GetFeatureManager()
	fm.LoadFromDB()

	jobQueue = make(chan func(), jobQueueSize)
	var wg sync.WaitGroup
	startWorkerPool(ctx, &wg)

	apiID, _ := strconv.Atoi(os.Getenv("TELEGRAM_API_ID"))
	apiHash := os.Getenv("TELEGRAM_API_HASH")
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	botUsername := os.Getenv("TELEGRAM_BOT_USERNAME")
	if botUsername == "" {
		logger.Warn("TELEGRAM_BOT_USERNAME belum diset di .env")
	}

	rootIDStr := os.Getenv("OWNER_ID")
	var rootID int64 = 0
	if rootIDStr != "" {
		var err error
		rootID, err = strconv.ParseInt(rootIDStr, 10, 64)
		if err != nil {
			logger.Warn("Invalid OWNER_ID", zap.Error(err))
		}
	}

	dispatcher := tg.NewUpdateDispatcher()
	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		UpdateHandler:  dispatcher,
		SessionStorage: &session.FileStorage{Path: "session.json"},
	})

	dispatcher.OnNewMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewMessage) error {
		defer recoverLog(ctx, "OnNewMessage")
		return handleMessage(ctx, client.API(), update.Message, entities, rootID, botUsername)
	})
	dispatcher.OnNewChannelMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
		defer recoverLog(ctx, "OnNewChannelMessage")
		return handleMessage(ctx, client.API(), update.Message, entities, rootID, botUsername)
	})
	dispatcher.OnBotCallbackQuery(func(ctx context.Context, entities tg.Entities, update *tg.UpdateBotCallbackQuery) error {
		defer recoverLog(ctx, "OnBotCallbackQuery")
		return handleCallbackQuery(ctx, client.API(), update, logger)
	})
	dispatcher.OnBotGuestChatQuery(func(ctx context.Context, e tg.Entities, upd *tg.UpdateBotGuestChatQuery) error {
		defer recoverLog(ctx, "OnBotGuestChatQuery")
		return commands.HandleBotGuestChatQuery(ctx, client.API(), upd, logger)
	})

	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			for {
				_, authErr := client.Auth().Bot(ctx, botToken)
				if authErr == nil {
					break
				}
				if d, ok := tgerr.AsFloodWait(authErr); ok {
					logger.Warn("Auth FLOOD_WAIT, menunggu...", zap.Duration("wait", d))
					select {
					case <-time.After(d + time.Second):
						continue
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return authErr
			}
		}

		logGroupIDStr := os.Getenv("GROUP_ID")
		if logGroupIDStr != "" {
			logGroupID, err := strconv.ParseInt(logGroupIDStr, 10, 64)
			if err == nil {
				logPeer, err := getLogPeer(logGroupID)
				if err != nil {
					logger.Warn("Gagal dapatkan peer log group", zap.Error(err))
				} else {
					log.InitLogger(logPeer, client.API(), logger)
					log.LogInfo(ctx, "✅ Bot Kometika Lite started successfully")
				}
			}
		}

		// Inisialisasi Cache Group untuk Guest Mode
		cachePeer, err := getCachePeerFromEnv()
		if err != nil {
			logger.Warn("Cache group tidak diset, guest mode akan fallback ke Saved Messages", zap.Error(err))
		} else {
			commands.InitGuestMode(cachePeer)
			logger.Info("Cache group untuk Guest Mode berhasil diinisialisasi")
		}

		logger.Info("Bot Kometika Lite running")
		<-ctx.Done()
		return ctx.Err()
	})
	cancel()
	wg.Wait()
	logger.Info("Bot shutdown gracefully")

	if err != nil {
		logger.Fatal("Client run error", zap.Error(err))
	}
}

func getLogPeer(chatID int64) (tg.InputPeerClass, error) {
	if chatID > 0 {
		return &tg.InputPeerChat{ChatID: chatID}, nil
	}

	accessHashStr := os.Getenv("GROUP_HASH")
	if accessHashStr == "" {
		return nil, fmt.Errorf("GROUP_HASH tidak diset di .env")
	}
	accessHash, err := strconv.ParseInt(accessHashStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("GROUP_HASH tidak valid: %w", err)
	}

	const zeroChannelID int64 = -1_000_000_000_000
	channelID := -(chatID - zeroChannelID)

	return &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash}, nil
}

// getCachePeerFromEnv membaca GROUP_ID/GROUP_HASH dari .env dan mengembalikan InputPeerClass untuk cache guest mode
func getCachePeerFromEnv() (tg.InputPeerClass, error) {
	cacheGroupIDStr := os.Getenv("GROUP_ID")
	if cacheGroupIDStr == "" {
		return nil, fmt.Errorf("GROUP_ID tidak diset di .env")
	}

	chatID, err := strconv.ParseInt(cacheGroupIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("GROUP_ID tidak valid: %w", err)
	}

	if chatID > 0 {
		return &tg.InputPeerChat{ChatID: chatID}, nil
	}

	accessHashStr := os.Getenv("GROUP_HASH")
	if accessHashStr == "" {
		return nil, fmt.Errorf("GROUP_HASH tidak diset di .env untuk Channel/Supergroup")
	}

	accessHash, err := strconv.ParseInt(accessHashStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("GROUP_HASH tidak valid: %w", err)
	}

	const zeroChannelID int64 = -1_000_000_000_000
	channelID := -(chatID - zeroChannelID)

	return &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash}, nil
}

func handleMessage(ctx context.Context, tgClient *tg.Client, msgClass tg.MessageClass, entities tg.Entities, rootID int64, botUsername string) error {
	msg, ok := msgClass.(*tg.Message)
	if !ok || msg.Message == "" {
		return nil
	}
	text := msg.Message

	isCommand := false
	for _, prefix := range []string{"/", ".", "!", "#"} {
		if strings.HasPrefix(text, prefix) {
			isCommand = true
			break
		}
	}

	isBotMention := false
	for _, entity := range msg.Entities {
		if mention, ok := entity.(*tg.MessageEntityMention); ok {
			if mention.Offset+mention.Length <= len(text) {
				mentionedText := text[mention.Offset : mention.Offset+mention.Length]
				targetUsername := botUsername
				if !strings.HasPrefix(targetUsername, "@") {
					targetUsername = "@" + targetUsername
				}
				if strings.EqualFold(mentionedText, targetUsername) {
					isBotMention = true
					break
				}
			}
		}
	}
	_, isPrivate := msg.PeerID.(*tg.PeerUser)

	if !isCommand && isPrivate {
		handleAutoDownload(ctx, text, tgClient, msg, entities)
		return nil
	}

	user := getUserFromMessage(ctx, tgClient, msg, entities)
	if isCommand {
		return handleCommand(ctx, tgClient, msg, entities, user, rootID, botUsername, text)
	}
	if isBotMention {
		handleAutoDownload(ctx, text, tgClient, msg, entities)
		return nil
	}
	return nil
}

func handleCallbackQuery(ctx context.Context, tgClient *tg.Client, query *tg.UpdateBotCallbackQuery, logger *zap.Logger) error {
	data := query.Data

	if bytes.HasPrefix(data, []byte("tb_")) {
		peer, err := getPeerFromCallback(ctx, tgClient, query)
		if err != nil {
			logger.Error("Gagal dapat peer dari callback Terabox", zap.Error(err))
			return nil
		}
		return commands.HandleTeraboxCallback(context.Background(), tgClient, peer, query.MsgID, query, logger)
	}

	if !bytes.HasPrefix(data, []byte("mp3_")) {
		return nil
	}

	videoID := string(bytes.TrimPrefix(data, []byte("mp3_")))
	audioURL, title, musicName, ok := cache.GetAudio(videoID)
	if !ok {
		answer := &tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   query.QueryID,
			Message:   "❌ Link audio sudah kadaluarsa. Silakan minta ulang video TikTok.",
			CacheTime: 10,
		}
		_, _ = tgClient.MessagesSetBotCallbackAnswer(ctx, answer)
		return nil
	}

	peer, err := getPeerFromCallback(ctx, tgClient, query)
	if err != nil {
		logger.Error("Gagal dapat peer dari callback", zap.Error(err))
		return nil
	}

	answer := &tg.MessagesSetBotCallbackAnswerRequest{
		QueryID:   query.QueryID,
		Message:   "⏳ Mengunduh audio, mohon tunggu...",
		CacheTime: 5,
	}
	_, _ = tgClient.MessagesSetBotCallbackAnswer(ctx, answer)

	bgCtx := context.Background()
	stream, _, err := api.GetAudioStream(bgCtx, audioURL)
	if err != nil {
		logger.Error("Gagal download audio", zap.Error(err))
		media.SendTextMessage(ctx, tgClient, peer, "❌ Gagal mengunduh audio.")
		return nil
	}
	defer stream.Close()
	mediaSender := media.NewMediaSender(tgClient)
	msgSender := message.NewSender(tgClient)

	filename := fmt.Sprintf("%s.mp3", videoID)
	caption := fmt.Sprintf("🎵 %s - %s", musicName, title)
	if len(caption) > 200 {
		caption = caption[:200]
	}
	replyTo := &tg.InputReplyToMessage{
		ReplyToMsgID: query.MsgID,
	}

	err = mediaSender.SendAudioStream(bgCtx, peer, stream, filename, title, musicName, caption, nil, replyTo)
	if err != nil {
		logger.Error("Gagal kirim audio", zap.Error(err))
		_, _ = msgSender.To(peer).Text(ctx, "❌ Gagal mengunduh audio.")
		return nil
	}

	logger.Info("Audio berhasil dikirim", zap.String("video_id", videoID))
	return nil
}

func getPeerFromCallback(ctx context.Context, tgClient *tg.Client, query *tg.UpdateBotCallbackQuery) (tg.InputPeerClass, error) {
	switch p := query.Peer.(type) {
	case *tg.PeerUser:
		users, err := tgClient.UsersGetUsers(ctx, []tg.InputUserClass{
			&tg.InputUser{UserID: p.UserID},
		})
		if err != nil || len(users) == 0 {
			return nil, err
		}
		u, ok := users[0].(*tg.User)
		if !ok || u.AccessHash == 0 {
			return nil, fmt.Errorf("user %d tidak valid dari callback", p.UserID)
		}
		return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, nil
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, nil
	case *tg.PeerChannel:
		ch, err := tgClient.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: p.ChannelID},
		})
		if err != nil || len(ch.GetChats()) == 0 {
			return nil, err
		}
		c, ok := ch.GetChats()[0].(*tg.Channel)
		if !ok || c.AccessHash == 0 {
			return nil, fmt.Errorf("channel %d tidak valid dari callback", p.ChannelID)
		}
		return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}, nil
	}
	return nil, nil
}
