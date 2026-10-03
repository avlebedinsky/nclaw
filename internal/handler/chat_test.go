package handler

import (
	"context"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/telegram"
)

const groupID = -1001234567890

func groupMessage(thread int, text string) *models.Message {
	return &models.Message{
		ID: 50, Text: text, MessageThreadID: thread,
		Chat: models.Chat{ID: groupID, Type: models.ChatTypeSupergroup, Title: "Семья"},
		From: &models.User{ID: 7, FirstName: "Анна", LastName: "Л."},
	}
}

func TestDefault_RemembersGroupAndTopicFromTopicRoot(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	msg := groupMessage(1224, "сколько потратили?")
	msg.ReplyToMessage = &models.Message{ID: 1224, ForumTopicCreated: &models.ForumTopicCreated{Name: "Финансы"}}

	h.Default(context.Background(), nil, &models.Update{Message: msg})

	assert.Equal(t, "Семья", telegram.ChatName(h.Invoker.ChatDir(groupID, 0)))
	assert.Equal(t, "Финансы", telegram.TopicName(h.Invoker.ChatDir(groupID, 1224)))
}

func TestDefault_LearnsTopicFromServiceMessages(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	created := groupMessage(80, "")
	created.ForumTopicCreated = &models.ForumTopicCreated{Name: "Торренты"}
	h.Default(context.Background(), nil, &models.Update{Message: created})
	assert.Equal(t, "Торренты", telegram.TopicName(h.Invoker.ChatDir(groupID, 80)))

	renamed := groupMessage(80, "")
	renamed.ForumTopicEdited = &models.ForumTopicEdited{Name: "Кино"}
	h.Default(context.Background(), nil, &models.Update{Message: renamed})
	assert.Equal(t, "Кино", telegram.TopicName(h.Invoker.ChatDir(groupID, 80)))

	iconOnly := groupMessage(80, "")
	iconOnly.ForumTopicEdited = &models.ForumTopicEdited{IconCustomEmojiID: "123"}
	h.Default(context.Background(), nil, &models.Update{Message: iconOnly})
	assert.Equal(t, "Кино", telegram.TopicName(h.Invoker.ChatDir(groupID, 80)))
	assert.Equal(t, 0, h.Queue.Snapshot(chatKeyThread(groupID, 80)).PendingUser)
}

func TestDefault_RealReplyDoesNotRenameTopic(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	_, err := telegram.SaveTopicName(h.Invoker.ChatDir(groupID, 1224), "Финансы")
	require.NoError(t, err)
	msg := groupMessage(1224, "а это?")
	msg.ReplyToMessage = &models.Message{ID: 1300, Text: "выписка за август"}

	h.Default(context.Background(), nil, &models.Update{Message: msg})

	assert.Equal(t, "Финансы", telegram.TopicName(h.Invoker.ChatDir(groupID, 1224)))
}

func TestDefault_RemembersPersonInPrivateChat(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	msg := &models.Message{ID: 1, Text: "привет", Chat: models.Chat{ID: 396429376, Type: models.ChatTypePrivate, FirstName: "Анна"}}

	h.Default(context.Background(), nil, &models.Update{Message: msg})

	assert.Equal(t, "Анна", telegram.ChatName(h.Invoker.ChatDir(396429376, 0)))
}

func TestNewInbound_NamesSenderOnlyInGroups(t *testing.T) {
	in, ok := newInbound(groupMessage(0, "hi"))
	require.True(t, ok)
	assert.Equal(t, "Анна Л.", in.sender)

	private := &models.Message{Text: "hi", Chat: models.Chat{ID: 375321681, Type: models.ChatTypePrivate}, From: &models.User{FirstName: "Антон"}}
	in, ok = newInbound(private)
	require.True(t, ok)
	assert.Empty(t, in.sender)

	channelPost := groupMessage(0, "hi")
	channelPost.From = nil
	channelPost.SenderChat = &models.Chat{ID: -100777, Title: "Новости"}
	in, ok = newInbound(channelPost)
	require.True(t, ok)
	assert.Equal(t, "Новости", in.sender)
}

func TestComposePrompt_PrefixesEachSender(t *testing.T) {
	h := &Handler{}
	prompt := h.composePrompt(context.Background(), t.TempDir(), []Inbound{
		{sender: "Анна", text: "купи хлеб"},
		{sender: "Антон", text: "и молоко"},
	})

	assert.Contains(t, prompt, "2 messages arrived in a row")
	assert.Contains(t, prompt, "--- Message 1 ---\n[From: Анна]\nкупи хлеб")
	assert.Contains(t, prompt, "--- Message 2 ---\n[From: Антон]\nи молоко")
	assert.Equal(t, "hello", h.composePrompt(context.Background(), t.TempDir(), []Inbound{{text: "hello"}}))
}

func chatKeyThread(chatID int64, threadID int) chatqueue.Key {
	return chatqueue.Key{ChatID: chatID, ThreadID: threadID}
}
