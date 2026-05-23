package bot

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/pipeline"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/queue"
)

type Deps struct {
	Queue        *queue.Queue
	AllowedUsers map[int64]struct{} // empty = allow all
	Logger       *slog.Logger
}

// Notifier implements pipeline.Notifier on top of *bot.Bot.
type Notifier struct {
	B      *bot.Bot
	Logger *slog.Logger
}

func (n *Notifier) log() *slog.Logger {
	if n.Logger != nil {
		return n.Logger
	}
	return slog.Default()
}

func (n *Notifier) Progress(chatID int64, msgID int, text string) {
	if msgID == 0 {
		return
	}
	if _, err := n.B.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      text,
	}); err != nil {
		n.log().Warn("notifier progress", "chat_id", chatID, "msg_id", msgID, "err", err)
	}
}

func (n *Notifier) Done(chatID int64, msgID int) {
	if msgID == 0 {
		return
	}
	if _, err := n.B.DeleteMessage(context.Background(), &bot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: msgID,
	}); err != nil {
		n.log().Warn("notifier done", "chat_id", chatID, "msg_id", msgID, "err", err)
	}
}

func (n *Notifier) Error(chatID int64, msgID int, userMessage string) {
	if msgID == 0 {
		if _, err := n.B.SendMessage(context.Background(), &bot.SendMessageParams{
			ChatID: chatID,
			Text:   userMessage,
		}); err != nil {
			n.log().Warn("notifier error send", "chat_id", chatID, "err", err)
		}
		return
	}
	if _, err := n.B.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      "❌ " + userMessage,
	}); err != nil {
		n.log().Warn("notifier error edit", "chat_id", chatID, "msg_id", msgID, "err", err)
	}
}

func Handler(d Deps) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" {
			return
		}
		chatID := update.Message.Chat.ID
		userID := update.Message.From.ID
		originalMsgID := update.Message.ID
		text := update.Message.Text
		replyTo := &models.ReplyParameters{MessageID: originalMsgID}

		if strings.HasPrefix(text, "/start") {
			if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:          chatID,
				Text:            "Пришли ссылку на трек Spotify или видео YouTube (до 5 минут) — отвечу mp3.",
				ReplyParameters: replyTo,
			}); err != nil {
				d.Logger.Warn("send /start reply", "chat_id", chatID, "err", err)
			}
			return
		}

		if len(d.AllowedUsers) > 0 {
			if _, ok := d.AllowedUsers[userID]; !ok {
				d.Logger.Info("denied", "user_id", userID)
				return
			}
		}

		link, err := ParseLink(text)
		if err != nil {
			if _, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:          chatID,
				Text:            "пришли ссылку на трек Spotify или YouTube",
				ReplyParameters: replyTo,
			}); sendErr != nil {
				d.Logger.Warn("send parse-error reply", "chat_id", chatID, "err", sendErr)
			}
			return
		}

		if !d.Queue.TryAcquireUser(userID) {
			if _, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:          chatID,
				Text:            "жди, твой прошлый трек ещё качается",
				ReplyParameters: replyTo,
			}); sendErr != nil {
				d.Logger.Warn("send busy reply", "chat_id", chatID, "err", sendErr)
			}
			return
		}

		reply, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          chatID,
			Text:            "⏳ качаю…",
			ReplyParameters: replyTo,
		})
		var replyID int
		if err == nil && reply != nil {
			replyID = reply.ID
		}

		ok := d.Queue.Enqueue(queue.Job{
			ChatID:            chatID,
			UserID:            userID,
			Source:            link.Source,
			SourceID:          link.ID,
			SourceURL:         link.URL,
			ReplyMessageID:    replyID,
			OriginalMessageID: originalMsgID,
		})
		if !ok {
			d.Queue.ReleaseUser(userID)
			if replyID != 0 {
				if _, editErr := b.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    chatID,
					MessageID: replyID,
					Text:      "очередь переполнена, попробуй позже",
				}); editErr != nil {
					d.Logger.Warn("send queue-full reply", "chat_id", chatID, "err", editErr)
				}
			}
		}
	}
}

func ParseAllowedUsers(raw string) map[int64]struct{} {
	out := make(map[int64]struct{})
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

// Compile-time check that *Notifier satisfies pipeline.Notifier.
var _ pipeline.Notifier = (*Notifier)(nil)
