package commands

import (
	"context"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const logFilePath = "mybot.log"

// HandleGetLogCommand mengirim file log bot ke owner.
// Hanya private chat (DM): kalau owner memanggil di group/channel, abaikan.
// Non-owner juga diabaikan diam-diam — tidak ada balasan apa pun.
func HandleGetLogCommand(ctx context.Context, client *tg.Client, msg *tg.Message, entities tg.Entities, user *tg.User, rootID int64, logger *zap.Logger) error {
	if user == nil || user.ID != rootID {
		return nil
	}
	if _, isPrivate := msg.PeerID.(*tg.PeerUser); !isPrivate {
		return nil
	}

	peer, err := GetPeerFromMessage(ctx, client, msg, entities)
	if err != nil || peer == nil {
		return nil
	}

	up := uploader.NewUploader(client).WithThreads(2)
	file, err := up.FromPath(ctx, logFilePath)
	if err != nil {
		logger.Error("Gagal upload file log", zap.Error(err))
		return nil
	}

	_, err = client.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: "text/plain",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "mybot.log"},
			},
		},
		Message:  "📄 File log bot Kometika",
		RandomID: time.Now().UnixNano(),
	})
	if err != nil {
		logger.Error("Gagal kirim file log", zap.Error(err))
	}
	return nil
}
