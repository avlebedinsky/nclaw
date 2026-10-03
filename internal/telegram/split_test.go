package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertChunksFit(t *testing.T, chunks []string, maxLen int) {
	t.Helper()
	for _, c := range chunks {
		assert.True(t, utf8.ValidString(c), "chunk is not valid UTF-8: %q", c)
		assert.LessOrEqual(t, visibleWidth(tokenize(c)), maxLen, "chunk too long: %q", c)
	}
}

func TestSplitMessage_CyrillicCountsCharactersNotBytes(t *testing.T) {
	msg := strings.Repeat("я", 10)
	assert.Equal(t, []string{msg}, SplitMessage(msg, 10))
}

func TestSplitMessage_LongCyrillicLineKeepsRunesWhole(t *testing.T) {
	msg := strings.Repeat("привет", 10)
	chunks := SplitMessage(msg, 7)

	assertChunksFit(t, chunks, 7)
	assert.Equal(t, msg, strings.Join(chunks, ""))
}

func TestSplitMessage_EmojiCountsUTF16Units(t *testing.T) {
	chunks := SplitMessage(strings.Repeat("😀", 6), 5)

	assert.Equal(t, []string{"😀😀", "😀😀", "😀😀"}, chunks)
}

func TestSplitMessage_TagsDoNotCountTowardsLimit(t *testing.T) {
	msg := "<b>" + strings.Repeat("a", 10) + "</b>"
	assert.Equal(t, []string{msg}, SplitMessage(msg, 10))
}

func TestSplitMessage_PreBlockReopenedAcrossCut(t *testing.T) {
	msg := `<pre><code class="language-go">line1` + "\n" + `line2` + "\n" + `line3</code></pre>`

	chunks := SplitMessage(msg, 12)

	assert.Equal(t, []string{
		`<pre><code class="language-go">line1` + "\n" + `line2</code></pre>`,
		`<pre><code class="language-go">line3</code></pre>`,
	}, chunks)
}

func TestSplitMessage_LinkReopenedWithAttributes(t *testing.T) {
	msg := `<a href="https://x.y/?a=1&amp;b=2">` + strings.Repeat("w", 8) + `</a> tail`

	chunks := SplitMessage(msg, 5)

	require.Len(t, chunks, 3)
	assert.Equal(t, `<a href="https://x.y/?a=1&amp;b=2">wwwww</a>`, chunks[0])
	assert.Equal(t, `<a href="https://x.y/?a=1&amp;b=2">www</a> t`, chunks[1])
	assert.Equal(t, `ail`, chunks[2])
}

func TestSplitMessage_EntitiesAreSingleCharacters(t *testing.T) {
	chunks := SplitMessage(strings.Repeat("&lt;", 10), 5)

	assert.Equal(t, []string{strings.Repeat("&lt;", 5), strings.Repeat("&lt;", 5)}, chunks)
}

func TestSplitMessage_InvalidUTF8IsReplaced(t *testing.T) {
	msg := strings.Repeat("a", 6) + "\xff" + strings.Repeat("b", 6)

	chunks := SplitMessage(msg, 5)

	assertChunksFit(t, chunks, 5)
	assert.Contains(t, strings.Join(chunks, ""), "\uFFFD")
}

func TestSplitMessage_MixedContentStaysBalanced(t *testing.T) {
	var b strings.Builder
	for i := range 40 {
		b.WriteString("<b>Раздел</b> текст &amp; ещё <i>курсив <u>подчёркнутый</u></i>\n")
		if i%5 == 0 {
			b.WriteString("<pre>код\nстрока 2 😀\n</pre>\n")
		}
	}
	msg := b.String()

	chunks := SplitMessage(msg, 100)

	require.Greater(t, len(chunks), 1)
	assertChunksFit(t, chunks, 100)
	for _, c := range chunks {
		assert.Empty(t, applyTags(nil, tokenize(c)), "unbalanced tags in %q", c)
	}
	strip := func(s string) string { return strings.ReplaceAll(PlainText(s), "\n", "") }
	assert.Equal(t, strip(msg), strip(strings.Join(chunks, "")))
}

func TestSplitMessage_StrayAngleBracketIsText(t *testing.T) {
	chunks := SplitMessage("a < b and c > d", 5)

	assertChunksFit(t, chunks, 5)
	assert.Equal(t, "a < b and c > d", strings.Join(chunks, ""))
}

func TestPlainText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"tags and entities", `<b>bold</b> &amp; <a href="x">link</a> &lt;tag&gt;`, "bold & link <tag>"},
		{"stray bracket", "a < b", "a < b"},
		{"unsupported tag kept", "<div>x</div>", "<div>x</div>"},
		{"nested code", `<pre><code class="language-go">x := 1</code></pre>`, "x := 1"},
		{"numeric entity", "&#128512; &#x41;", "😀 A"},
		{"unknown entity kept", "&nosuch;", "&nosuch;"},
		{"uppercase tag", "<B>x</B>", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, PlainText(tc.in))
		})
	}
}
