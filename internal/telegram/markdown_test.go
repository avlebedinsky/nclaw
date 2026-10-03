package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarkdown(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"inline styles", "<b>bold</b>, <i>it</i>, <s>gone</s>, <u>under</u>", "**bold**, _it_, ~~gone~~, under"},
		{"entities", "a &lt; b &amp;&amp; c", "a < b && c"},
		{"inline code", "run <code>go test</code>", "run `go test`"},
		{"code block with language", "Code:<pre><code class=\"language-go\">x := 1\n</code></pre>done", "Code:\n```go\nx := 1\n\n```\ndone"},
		{"plain pre", "<pre>a  b\nc  d</pre>", "```\na  b\nc  d\n```"},
		{"link", `see <a href="https://x.y/?a=1&amp;b=2">docs</a>`, "see [docs](https://x.y/?a=1&b=2)"},
		{"blockquote", "intro\n<blockquote>line one\nline two</blockquote>after", "intro\n> line one\n> line two\nafter"},
		{"unknown tag kept", "<div>x</div>", "<div>x</div>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Markdown(tc.in))
		})
	}
}
