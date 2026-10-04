package progress

import (
	"context"

	"github.com/go-telegram/bot"

	"github.com/nickalie/nclaw/internal/buttons"
)

type botAPI struct {
	b *bot.Bot
}

// NewBotAPI adapts a Telegram bot to MessageAPI.
func NewBotAPI(b *bot.Bot) MessageAPI {
	return botAPI{b: b}
}

func (a botAPI) Send(ctx context.Context, chatID int64, threadID int, text string, kb buttons.Keyboard) (int, error) {
	params := &bot.SendMessageParams{ChatID: chatID, MessageThreadID: threadID, Text: text}
	if len(kb) > 0 {
		params.ReplyMarkup = buttons.Markup(kb)
	}
	msg, err := a.b.SendMessage(ctx, params)
	if err != nil {
		return 0, err
	}
	return msg.ID, nil
}

func (a botAPI) Edit(ctx context.Context, chatID int64, msgID int, text string, kb buttons.Keyboard) error {
	_, err := a.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: chatID, MessageID: msgID, Text: text, ReplyMarkup: buttons.Markup(kb),
	})
	return err
}

func (a botAPI) Delete(ctx context.Context, chatID int64, msgID int) error {
	_, err := a.b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: msgID})
	return err
}
