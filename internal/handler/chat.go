package handler

import (
	"log"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/telegram"
)

func (h *Handler) rememberChat(msg *models.Message) {
	chatID, threadID := msg.Chat.ID, msg.MessageThreadID
	h.saveName("chat", chatID, 0, chatName(msg), telegram.SaveChatName)
	if threadID != 0 {
		h.saveName("topic", chatID, threadID, topicName(msg), telegram.SaveTopicName)
	}
}

func (h *Handler) saveName(kind string, chatID int64, threadID int, name string, save func(dir, name string) (bool, error)) {
	changed, err := save(h.Invoker.ChatDir(chatID, threadID), name)
	if err != nil {
		log.Printf("handler: save %s name for chat=%d thread=%d: %v", kind, chatID, threadID, err)
		return
	}
	if changed {
		log.Printf("handler: %s name for chat=%d thread=%d is now %q", kind, chatID, threadID, name)
	}
}

func chatName(msg *models.Message) string {
	if telegram.IsPrivateChat(msg.Chat.ID) {
		return msg.Chat.FirstName + " " + msg.Chat.LastName
	}
	return msg.Chat.Title
}

func topicName(msg *models.Message) string {
	switch {
	case msg.ForumTopicCreated != nil:
		return msg.ForumTopicCreated.Name
	case msg.ForumTopicEdited != nil:
		return msg.ForumTopicEdited.Name
	case msg.ReplyToMessage != nil && msg.ReplyToMessage.ForumTopicCreated != nil:
		return msg.ReplyToMessage.ForumTopicCreated.Name
	default:
		return ""
	}
}

func senderName(msg *models.Message) string {
	switch {
	case telegram.IsPrivateChat(msg.Chat.ID):
		return ""
	case msg.From != nil:
		return personName(msg.From)
	case msg.SenderChat != nil:
		return msg.SenderChat.Title
	default:
		return ""
	}
}

func pressedBy(msg *models.Message, u *models.User) string {
	if telegram.IsPrivateChat(msg.Chat.ID) {
		return ""
	}
	return personName(u)
}

func personName(u *models.User) string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}
