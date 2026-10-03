package progress

import (
	"context"

	"github.com/go-telegram/bot"
)

type botAPI struct {
	b *bot.Bot
}

// NewBotAPI adapts a Telegram bot to MessageAPI.
func NewBotAPI(b *bot.Bot) MessageAPI {
	return botAPI{b: b}
}

func (a botAPI) Send(ctx context.Context, chatID int64, threadID int, text string) (int, error) {
	msg, err := a.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, MessageThreadID: threadID, Text: text})
	if err != nil {
		return 0, err
	}
	return msg.ID, nil
}

func (a botAPI) Edit(ctx context.Context, chatID int64, msgID int, text string) error {
	_, err := a.b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: text})
	return err
}

func (a botAPI) Delete(ctx context.Context, chatID int64, msgID int) error {
	_, err := a.b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: msgID})
	return err
}
