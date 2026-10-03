package blocks

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
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
