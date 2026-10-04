package blocks

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatterns(t *testing.T) {
	cases := []struct {
		name string
		re   *regexp.Regexp
		tag  string
	}{
		{"schedule", Schedule, "schedule"},
		{"webhook", Webhook, "webhook"},
		{"sendfile", SendFile, "sendfile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := "a\n```nclaw:" + tc.tag + "\n{\"x\":1}\n```\nb\n```nclaw:" + tc.tag + "\n{\"x\":2}\n```"
			matches := tc.re.FindAllStringSubmatch(input, -1)
			assert.Len(t, matches, 2)
			assert.Equal(t, "{\"x\":1}", matches[0][1])
			assert.Equal(t, "{\"x\":2}", matches[1][1])
			assert.Empty(t, tc.re.FindAllStringSubmatch("text\n```go\nfmt.Println(1)\n```", -1))
		})
	}
}

func TestStripAll(t *testing.T) {
	text := "before\n```nclaw:schedule\n{}\n```\nmiddle\n```nclaw:webhook\n{}\n```\n" +
		"```nclaw:sendfile\n{\"path\":\"a.txt\"}\n```\nafter"
	assert.Equal(t, "before\n\nmiddle\n\n\nafter", StripAll(text))
}

func TestStripAll_NoBlocks(t *testing.T) {
	assert.Equal(t, "plain text", StripAll("  plain text\n"))
}

func TestStripAll_OnlyBlocks(t *testing.T) {
	assert.Empty(t, StripAll("```nclaw:sendfile\n{\"path\":\"/tmp/a\"}\n```"))
}

func TestStripAll_KeepsOtherCodeBlocks(t *testing.T) {
	text := "```go\nfmt.Println(1)\n```"
	assert.Equal(t, text, StripAll(text))
}

func TestButtonLabels(t *testing.T) {
	text := "Удалить?\n```nclaw:buttons\n[\" Да \", \"\", \"Нет\"]\n```"
	assert.Equal(t, []string{"Да", "Нет"}, ButtonLabels(text))
	assert.Equal(t, "Удалить?", StripAll(text))
}

func TestButtonLabels_LastBlockLimitsAndLongLabels(t *testing.T) {
	text := "```nclaw:buttons\n[\"old\"]\n```\n```nclaw:buttons\n" +
		"[\"1\",\"2\",\"3\",\"4\",\"5\",\"6\",\"7\",\"" + strings.Repeat("я", 50) + "\"]\n```"
	labels := ButtonLabels(text)

	assert.Equal(t, []string{"1", "2", "3", "4", "5", "6"}, labels)
	long := ButtonLabels("```nclaw:buttons\n[\"" + strings.Repeat("я", 50) + "\"]\n```")
	require.Len(t, long, 1)
	assert.Equal(t, strings.Repeat("я", 39)+"…", long[0])
}

func TestButtonLabels_MalformedOrMissing(t *testing.T) {
	assert.Nil(t, ButtonLabels("no block"))
	assert.Nil(t, ButtonLabels("```nclaw:buttons\n{\"a\":1}\n```"))
}

func TestStripPartial(t *testing.T) {
	assert.Equal(t, "Удалить?", StripPartial("Удалить?\n```nclaw:buttons\n[\"Да\""))
	assert.Equal(t, "a\n\nb", StripPartial("a\n```nclaw:schedule\n{}\n```\nb"))
	assert.Equal(t, "code ```go\nx", StripPartial("code ```go\nx"))
}
