package telegram

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	chatNameFile  = ".nclaw-chat-name"
	topicNameFile = ".nclaw-topic-name"
	maxNameRunes  = 128
)

// IsPrivateChat reports whether chatID is a private chat with a user: Telegram gives
// users positive IDs and groups and channels negative ones.
func IsPrivateChat(chatID int64) bool {
	return chatID > 0
}

// SaveChatName records the chat's title, or the person's name in a private chat, in the
// chat's directory. It reports whether the recorded name changed; an empty name is ignored.
func SaveChatName(chatDir, name string) (bool, error) {
	return saveName(chatDir, chatNameFile, name)
}

// SaveTopicName records a topic's name in the topic's directory. It reports whether the
// recorded name changed; an empty name is ignored.
func SaveTopicName(topicDir, name string) (bool, error) {
	return saveName(topicDir, topicNameFile, name)
}

// ChatName returns the name recorded by SaveChatName, or "" when there is none.
func ChatName(chatDir string) string {
	return readName(chatDir, chatNameFile)
}

// TopicName returns the name recorded by SaveTopicName, or "" when there is none.
func TopicName(topicDir string) string {
	return readName(topicDir, topicNameFile)
}

func saveName(dir, file, name string) (bool, error) {
	name = cleanName(name)
	if name == "" || readName(dir, file) == name {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp := filepath.Join(dir, file+".tmp")
	if err := os.WriteFile(tmp, []byte(name+"\n"), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, filepath.Join(dir, file))
}

func readName(dir, file string) string {
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	return cleanName(string(data))
}

func cleanName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > maxNameRunes {
		name = string(runes[:maxNameRunes])
	}
	return name
}
