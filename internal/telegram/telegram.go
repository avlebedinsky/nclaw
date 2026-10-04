package telegram

import (
	"fmt"
	"path/filepath"
)

// Prompt is the system prompt for formatting output as Telegram HTML and offering answers as buttons.
const Prompt = formattingPrompt + "\n\n" + buttonsPrompt

const buttonsPrompt = "When the user's next step is a short closed choice (yes or no, one of a few options), " +
	"end your reply with a block like\n" +
	"```nclaw:buttons\n[\"Yes, delete\", \"No\"]\n```\n" +
	"The options appear as buttons under your message: at most 6, short labels in the user's language. " +
	"The label of the pressed button comes back as the user's next message. The block itself is not shown. " +
	"Use buttons only when a tap is easier than typing; never for open questions."

const formattingPrompt = `IMPORTANT: Your output will be displayed in Telegram.
Format all responses using Telegram HTML. Supported tags:
<b>bold</b>, <i>italic</i>, <u>underline</u>, <s>strikethrough</s>,
<code>inline code</code>, <pre>code block</pre>, <pre><code class="language-go">code with language</code></pre>,
<a href="URL">link</a>, <blockquote>quote</blockquote>, <tg-spoiler>spoiler</tg-spoiler>

Rules:
- Do NOT use Markdown syntax (no #headers, no **bold**, no backticks for code)
- Use ONLY the HTML tags listed above. No other HTML tags are supported.
- Escape &, < and > in regular text as &amp; &lt; &gt; (but not inside tags themselves)
- Do NOT use <p>, <br>, <div>, <h1>-<h6>, <ul>, <li>, <ol>, <table>, or any other HTML tags
- For lists, use plain text with bullet characters or numbers
- Telegram cannot display tables (neither HTML nor Markdown ones):
  put tabular data inside <pre> with columns aligned by spaces, or use a list
- For section titles, use <b>bold text</b> on its own line
- Keep formatting minimal and clean

When a tool or connector fails because it has to be signed in again (re-authorization
required, expired OAuth, 401), do not retry it: tell the user in one line which connector
to reconnect and where (claude.ai connectors: claude.ai → Settings → Connectors), then
carry on without it.`

// MaxMessageLen is the Telegram message size limit in characters.
const MaxMessageLen = 4096

// ChatDir returns the session directory for a given chat/thread under the base data directory.
func ChatDir(dataDir string, chatID int64, threadID int) string {
	dir := filepath.Join(dataDir, fmt.Sprintf("%d", chatID))
	if threadID != 0 {
		dir = filepath.Join(dir, fmt.Sprintf("%d", threadID))
	}
	return dir
}
