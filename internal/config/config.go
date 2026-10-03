package config

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

var requiredKeys = []string{
	"telegram.bot_token",
	"data_dir",
}

// Init loads configuration from files and environment variables.
func Init() error {
	godotenv.Load() //nolint:errcheck // .env is optional; absence is not an error

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.nclaw")
	viper.AutomaticEnv()
	viper.SetEnvPrefix("NCLAW")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return err
		}
	}

	for _, key := range requiredKeys {
		if viper.GetString(key) == "" {
			envKey := "NCLAW_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
			return fmt.Errorf("%s is required (set %s env var or %s in config)", key, envKey, key)
		}
	}

	return nil
}

// TelegramBotToken returns the configured Telegram bot token.
func TelegramBotToken() string {
	return viper.GetString("telegram.bot_token")
}

// WhitelistChatIDs returns the list of allowed Telegram chat IDs.
func WhitelistChatIDs() []int64 {
	raw := viper.GetString("telegram.whitelist_chat_ids")
	var ids []int64
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		} else {
			log.Printf("config: ignoring invalid whitelist chat ID %q: %v", s, err)
		}
	}
	return ids
}

// AdminChatID returns the chat that receives the bot's own alerts, such as an expiring
// sign-in (env: NCLAW_ADMIN_CHAT_ID). By default it is the first private chat (positive
// ID) in the whitelist; 0 means there is none.
func AdminChatID() int64 {
	if viper.IsSet("admin_chat_id") {
		return viper.GetInt64("admin_chat_id")
	}
	for _, id := range WhitelistChatIDs() {
		if id > 0 {
			return id
		}
	}
	return 0
}

// LoginEmail returns the account email pre-filled on the sign-in page opened by /login
// (env: NCLAW_LOGIN_EMAIL).
func LoginEmail() string {
	return viper.GetString("login_email")
}

// StartupNotification reports whether the bot should send a startup
// notification message to whitelisted chats (env: NCLAW_STARTUP_NOTIFICATION).
// Disabled by default.
func StartupNotification() bool {
	return viper.GetBool("startup_notification")
}

// Progress reports whether a status message tracking the agent's tool calls is shown
// while a request runs (env: NCLAW_PROGRESS). Enabled by default.
func Progress() bool {
	if !viper.IsSet("progress") {
		return true
	}
	return viper.GetBool("progress")
}

// LiveDrafts reports whether private chats see the answer being written in a live
// Telegram draft (env: NCLAW_LIVE_DRAFTS). Enabled by default.
func LiveDrafts() bool {
	if !viper.IsSet("live_drafts") {
		return true
	}
	return viper.GetBool("live_drafts")
}

// Reactions reports whether the bot marks messages with reactions while it queues,
// works on and answers them (env: NCLAW_REACTIONS). Enabled by default.
func Reactions() bool {
	if !viper.IsSet("reactions") {
		return true
	}
	return viper.GetBool("reactions")
}

// StreamMessages reports whether every assistant message from the CLI's JSON
// stream should be sent as a separate Telegram reply (env: NCLAW_STREAM_MESSAGES).
// When disabled (default), only the final message is sent.
func StreamMessages() bool {
	return viper.GetBool("stream_messages")
}

// BundledSkillsDir returns the directory holding the skills shipped with the image
// (env: NCLAW_BUNDLED_SKILLS_DIR, default /opt/nclaw-skills).
func BundledSkillsDir() string {
	if dir := viper.GetString("bundled_skills_dir"); dir != "" {
		return dir
	}
	return "/opt/nclaw-skills"
}

// WhisperBin returns the whisper.cpp CLI used for voice transcription (env: NCLAW_WHISPER_BIN, default whisper-cli).
func WhisperBin() string {
	if bin := viper.GetString("whisper.bin"); bin != "" {
		return bin
	}
	return "whisper-cli"
}

// WhisperModel returns the ggml Whisper model path; empty disables transcription (env: NCLAW_WHISPER_MODEL).
func WhisperModel() string {
	return viper.GetString("whisper.model")
}

// WhisperLanguage returns the spoken-language hint for Whisper (env: NCLAW_WHISPER_LANGUAGE, default auto).
func WhisperLanguage() string {
	if lang := viper.GetString("whisper.language"); lang != "" {
		return lang
	}
	return "auto"
}

// DataDir returns the configured data directory path.
func DataDir() string {
	return viper.GetString("data_dir")
}

// DBPath returns the path to the SQLite database file.
func DBPath() string {
	if p := viper.GetString("db_path"); p != "" {
		return p
	}
	return filepath.Join(DataDir(), "nclaw.db")
}

// MaxSessionBytes returns the Claude Code session transcript size (in bytes)
// past which nclaw archives the session and starts a fresh conversation for
// that chat/thread on the next message. 0 (the default) disables automatic
// session resetting.
func MaxSessionBytes() int64 {
	return viper.GetInt64("max_session_bytes")
}

// WebhookBaseDomain returns the configured base domain for webhook URLs.
func WebhookBaseDomain() string {
	return viper.GetString("webhook.base_domain")
}

// WebhookPort returns the configured webhook server listen address, defaulting to ":3000".
func WebhookPort() string {
	if p := viper.GetString("webhook.port"); p != "" {
		return p
	}
	return ":3000"
}

// LogSecurityWarnings logs warnings for security-sensitive configuration.
func LogSecurityWarnings() {
	if len(WhitelistChatIDs()) == 0 {
		log.Println("WARNING: telegram.whitelist_chat_ids is not set — bot will accept messages from ANY chat")
	}
}

// CLI returns the configured CLI backend name (default: "claude").
// If "cli" is not explicitly set but "model" is set, returns "claudish" (auto-detection).
// Valid values: "claude", "codex", "copilot", "claudish", "gemini".
func CLI() string {
	if v := viper.GetString("cli"); v != "" {
		return strings.ToLower(v)
	}
	if viper.GetString("model") != "" {
		return "claudish"
	}
	return "claude"
}

// ValidCLIBackends returns the list of supported CLI backend names.
func ValidCLIBackends() []string {
	return []string{"claude", "claudish", "codex", "copilot", "gemini"}
}

// ClaudeExecPath returns the configured full path to the Claude CLI binary (env: NCLAW_CLAUDE_EXEC_PATH).
func ClaudeExecPath() string {
	return viper.GetString("claude_exec_path")
}

// Model returns the configured model name (env: NCLAW_MODEL).
func Model() string {
	return viper.GetString("model")
}

// ModelOpus returns the configured Opus-tier model override (env: NCLAW_MODEL_OPUS).
func ModelOpus() string {
	return viper.GetString("model_opus")
}

// ModelSonnet returns the configured Sonnet-tier model override (env: NCLAW_MODEL_SONNET).
func ModelSonnet() string {
	return viper.GetString("model_sonnet")
}

// ModelHaiku returns the configured Haiku-tier model override (env: NCLAW_MODEL_HAIKU).
func ModelHaiku() string {
	return viper.GetString("model_haiku")
}

// ModelSubagent returns the configured subagent model override (env: NCLAW_MODEL_SUBAGENT).
func ModelSubagent() string {
	return viper.GetString("model_subagent")
}

// CopilotModel returns the model for the Copilot CLI backend (env: NCLAW_COPILOT_MODEL).
func CopilotModel() string {
	return viper.GetString("copilot_model")
}

const defaultCLITimeout = 60 * time.Minute

// CLITimeout returns the maximum duration of one CLI run (env: NCLAW_CLI_TIMEOUT).
// A bare number is read as seconds, 0 disables the limit, and the default is 60 minutes.
func CLITimeout() time.Duration {
	raw := strings.TrimSpace(viper.GetString("cli_timeout"))
	if raw == "" {
		return defaultCLITimeout
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		log.Printf("config: invalid cli_timeout %q, using %s", raw, defaultCLITimeout)
		return defaultCLITimeout
	}
	return d
}

// Location returns the configured timezone, falling back to the system local zone when it is invalid.
func Location() *time.Location {
	loc, err := time.LoadLocation(Timezone())
	if err != nil {
		log.Printf("config: invalid timezone %q, falling back to local: %v", Timezone(), err)
		return time.Local
	}
	return loc
}

// Timezone returns the configured timezone name, defaulting to system local.
func Timezone() string {
	if tz := viper.GetString("timezone"); tz != "" {
		return tz
	}
	return time.Now().Location().String()
}
