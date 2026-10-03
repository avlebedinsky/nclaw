package telegram

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	tagRe    = regexp.MustCompile(`^<(/?)([a-zA-Z][a-zA-Z0-9-]*)(?:\s[^<>]*)?>`)
	entityRe = regexp.MustCompile(`^&(?:#\d+|#[xX][0-9a-fA-F]+|[a-zA-Z]+);`)
)

var telegramTags = map[string]bool{
	"b": true, "strong": true, "i": true, "em": true, "u": true, "ins": true,
	"s": true, "strike": true, "del": true, "span": true, "tg-spoiler": true,
	"a": true, "code": true, "pre": true, "blockquote": true, "tg-emoji": true,
}

type token struct {
	raw     string
	text    string
	width   int
	tag     string
	closing bool
}

func (t token) isTag() bool     { return t.tag != "" }
func (t token) isNewline() bool { return t.raw == "\n" }

// SplitMessage splits Telegram-HTML text into chunks whose visible text fits in maxLen
// UTF-16 code units. It prefers newline boundaries, never cuts through a tag, entity or
// character, and closes tags still open at a cut, reopening them in the next chunk.
func SplitMessage(text string, maxLen int) []string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	toks := tokenize(text)
	if visibleWidth(toks) <= maxLen {
		return []string{text}
	}

	var chunks []string
	var open []token
	for len(toks) > 0 {
		n, skip := cutPoint(toks, maxLen)
		body := toks[:n]
		if visibleWidth(body) > 0 {
			chunks = append(chunks, render(open, body))
		}
		open = applyTags(open, body)
		toks = trimLeadingNewlines(toks[n+skip:])
	}
	return chunks
}

// PlainText converts Telegram HTML into plain text by dropping supported tags and
// decoding entities, for sending a message without a parse mode.
func PlainText(htmlText string) string {
	var b strings.Builder
	for _, t := range tokenize(strings.ToValidUTF8(htmlText, "\uFFFD")) {
		if !t.isTag() {
			b.WriteString(t.text)
		}
	}
	return b.String()
}

func tokenize(text string) []token {
	var toks []token
	for text != "" {
		t := nextToken(text)
		toks = append(toks, t)
		text = text[len(t.raw):]
	}
	return toks
}

func nextToken(text string) token {
	switch text[0] {
	case '<':
		if m := tagRe.FindStringSubmatch(text); m != nil && telegramTags[strings.ToLower(m[2])] {
			return token{raw: m[0], tag: strings.ToLower(m[2]), closing: m[1] == "/"}
		}
	case '&':
		if raw := entityRe.FindString(text); raw != "" {
			if decoded := html.UnescapeString(raw); decoded != raw {
				return token{raw: raw, text: decoded, width: utf16Len(decoded)}
			}
		}
	}
	r, size := utf8.DecodeRuneInString(text)
	return token{raw: text[:size], text: text[:size], width: utf16.RuneLen(r)}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

func visibleWidth(toks []token) int {
	w := 0
	for _, t := range toks {
		w += t.width
	}
	return w
}

func cutPoint(toks []token, maxLen int) (n, skip int) {
	width, lastNewline := 0, -1
	for i, t := range toks {
		if width+t.width > maxLen {
			return overflowCut(i, lastNewline, t.isNewline())
		}
		if t.isNewline() && i > 0 {
			lastNewline = i
		}
		width += t.width
	}
	return len(toks), 0
}

func overflowCut(i, lastNewline int, atNewline bool) (n, skip int) {
	switch {
	case atNewline:
		return i, 1
	case lastNewline > 0:
		return lastNewline, 1
	case i == 0:
		return 1, 0
	default:
		return i, 0
	}
}

func render(open, body []token) string {
	var b strings.Builder
	for _, t := range open {
		b.WriteString(t.raw)
	}
	for _, t := range body {
		b.WriteString(t.raw)
	}
	closing := applyTags(open, body)
	for i := len(closing) - 1; i >= 0; i-- {
		b.WriteString("</" + closing[i].tag + ">")
	}
	return b.String()
}

func applyTags(open, toks []token) []token {
	stack := append([]token(nil), open...)
	for _, t := range toks {
		switch {
		case !t.isTag():
		case !t.closing:
			stack = append(stack, t)
		default:
			stack = popTag(stack, t.tag)
		}
	}
	return stack
}

func popTag(stack []token, tag string) []token {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].tag == tag {
			return append(stack[:i], stack[i+1:]...)
		}
	}
	return stack
}

func trimLeadingNewlines(toks []token) []token {
	for len(toks) > 0 && toks[0].isNewline() {
		toks = toks[1:]
	}
	return toks
}
