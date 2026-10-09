package handler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
)

var loginWindow = 10 * time.Minute

// Logins runs interactive sign-ins of the CLI backend started with /login from the admin chat.
type Logins struct {
	Provider    cli.LoginProvider
	AdminChatID int64

	mu      sync.Mutex
	pending map[int64]*pendingLogin
}

type pendingLogin struct {
	session cli.LoginSession
	cancel  context.CancelFunc
}

func (l *Logins) put(chatID int64, p *pendingLogin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		l.pending = make(map[int64]*pendingLogin)
	}
	if prev := l.pending[chatID]; prev != nil {
		prev.cancel()
	}
	l.pending[chatID] = p
}

func (l *Logins) take(chatID int64, want *pendingLogin) *pendingLogin {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.pending[chatID]
	if p == nil || (want != nil && p != want) {
		return nil
	}
	delete(l.pending, chatID)
	return p
}

func (h *Handler) login(key chatqueue.Key) {
	if key.ChatID != h.Logins.AdminChatID {
		go h.notify(key, "/login работает только в личном чате администратора с ботом.")
		return
	}
	go h.startLogin(key)
}

func (h *Handler) startLogin(key chatqueue.Key) {
	ctx, cancel := context.WithTimeout(context.Background(), loginWindow)
	session, err := h.Logins.Provider.StartLogin(ctx)
	if err != nil {
		cancel()
		log.Printf("handler: start login: %v", err)
		h.notify(key, "Не удалось начать вход: "+err.Error())
		return
	}
	p := &pendingLogin{session: session, cancel: cancel}
	h.Logins.put(key.ChatID, p)
	context.AfterFunc(ctx, func() {
		if h.Logins.take(key.ChatID, p) != nil {
			h.notify(key, "⌛ Вход не завершён вовремя. Отправьте /login, чтобы начать заново.")
		}
	})
	h.notify(key, "🔑 Откройте ссылку и войдите в аккаунт бота:\n"+session.URL()+
		"\n\nПотом пришлите следующим сообщением код, который покажут в конце. Ссылка действует 10 минут.")
}

func (h *Handler) takeLoginCode(msg *models.Message) bool {
	if h.Logins == nil || msg.Text == "" || strings.HasPrefix(msg.Text, "/") {
		return false
	}
	p := h.Logins.take(msg.Chat.ID, nil)
	if p == nil {
		return false
	}
	key := chatqueue.Key{ChatID: msg.Chat.ID, ThreadID: msg.MessageThreadID}
	log.Printf("handler: sign-in code received in chat=%d", key.ChatID)
	go h.finishLogin(key, p, msg.Text)
	return true
}

func (h *Handler) finishLogin(key chatqueue.Key, p *pendingLogin, code string) {
	defer p.cancel()
	if err := p.session.Submit(code); err != nil {
		log.Printf("handler: sign-in failed: %v", err)
		h.notify(key, "❌ Войти не удалось: "+err.Error()+"\nОтправьте /login, чтобы попробовать ещё раз.")
		return
	}
	log.Printf("handler: signed in from chat=%d", key.ChatID)
	text := "✅ Вход выполнен."
	if h.AuthStatus != nil {
		if line := h.AuthStatus(); line != "" {
			text += "\n" + line
		}
	}
	h.notify(key, text)
}
