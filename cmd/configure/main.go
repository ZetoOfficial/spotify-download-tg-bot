// Command configure applies the bot's display metadata (name, descriptions,
// commands menu) to Telegram via the Bot API. Run once, or whenever the
// content below changes:
//
//	make configure
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
)

const (
	botName          = "Spotify Downloader"
	shortDescription = "Пришли ссылку на трек в Spotify — отвечу mp3 с обложкой."
	longDescription  = "Бот для скачивания музыки из Spotify.\n\n" +
		"Как пользоваться:\n" +
		"Скинь ссылку на трек (open.spotify.com/track/…) — я пришлю mp3 с обложкой и тегами. " +
		"Повторная отправка той же ссылки — мгновенно из кэша."
)

var commands = []models.BotCommand{
	{Command: "start", Description: "Как пользоваться ботом"},
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("configure failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	_ = godotenv.Load() //nolint:errcheck // .env is optional

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return errors.New("TELEGRAM_BOT_TOKEN required")
	}

	b, err := tgbot.New(token)
	if err != nil {
		return fmt.Errorf("bot init: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := apply(ctx, b); err != nil {
		return err
	}
	logger.Info("bot metadata applied",
		"name", botName,
		"commands", len(commands),
	)
	return nil
}

func apply(ctx context.Context, b *tgbot.Bot) error {
	steps := []struct {
		name string
		do   func() error
	}{
		{"SetMyName", func() error {
			_, err := b.SetMyName(ctx, &tgbot.SetMyNameParams{Name: botName})
			return err
		}},
		{"SetMyShortDescription", func() error {
			_, err := b.SetMyShortDescription(ctx, &tgbot.SetMyShortDescriptionParams{
				ShortDescription: shortDescription,
			})
			return err
		}},
		{"SetMyDescription", func() error {
			_, err := b.SetMyDescription(ctx, &tgbot.SetMyDescriptionParams{
				Description: longDescription,
			})
			return err
		}},
		{"SetMyCommands", func() error {
			_, err := b.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
				Commands: commands,
			})
			return err
		}},
	}
	var errs []error
	for _, s := range steps {
		if err := s.do(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
		}
	}
	return errors.Join(errs...)
}
