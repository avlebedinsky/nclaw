package telegram

import (
	"html"
	"regexp"
	"strings"
)

var (
	hrefRe     = regexp.MustCompile(`(?i)\bhref\s*=\s*"([^"]*)"`)
	languageRe = regexp.MustCompile(`(?i)\bclass\s*=\s*"language-([^"\s]+)"`)
)

var markdownMarks = map[string]string{
	"b": "**", "strong": "**", "i": "_", "em": "_", "s": "~~", "strike": "~~", "del": "~~",
}

type mdWriter struct {
	b      strings.Builder
	hrefs  []string
	quotes []int
	inPre  bool
}

// Markdown converts Telegram HTML into Markdown, for sending a long answer as a file.
func Markdown(htmlText string) string {
	toks := tokenize(strings.ToValidUTF8(htmlText, "\uFFFD"))
	w := &mdWriter{}
	for i, t := range toks {
		if !t.isTag() {
			w.b.WriteString(t.text)
			continue
		}
		w.tag(t, toks[i+1:])
	}
	return strings.TrimSpace(w.b.String())
}

func (w *mdWriter) tag(t token, rest []token) {
	switch t.tag {
	case "pre":
		w.pre(t, rest)
	case "code":
		if !w.inPre {
			w.b.WriteString("`")
		}
	case "a":
		w.link(t)
	case "blockquote":
		w.blockquote(t)
	default:
		w.b.WriteString(markdownMarks[t.tag])
	}
}

func (w *mdWriter) pre(t token, rest []token) {
	if t.closing {
		w.inPre = false
		w.b.WriteString("\n```\n")
		return
	}
	w.inPre = true
	lang := ""
	if len(rest) > 0 && rest[0].tag == "code" && !rest[0].closing {
		if m := languageRe.FindStringSubmatch(rest[0].raw); m != nil {
			lang = m[1]
		}
	}
	w.b.WriteString("\n```" + lang + "\n")
}

func (w *mdWriter) link(t token) {
	if !t.closing {
		href := ""
		if m := hrefRe.FindStringSubmatch(t.raw); m != nil {
			href = html.UnescapeString(m[1])
		}
		w.hrefs = append(w.hrefs, href)
		w.b.WriteString("[")
		return
	}
	if len(w.hrefs) == 0 {
		return
	}
	href := w.hrefs[len(w.hrefs)-1]
	w.hrefs = w.hrefs[:len(w.hrefs)-1]
	w.b.WriteString("](" + href + ")")
}

func (w *mdWriter) blockquote(t token) {
	if !t.closing {
		w.quotes = append(w.quotes, w.b.Len())
		return
	}
	if len(w.quotes) == 0 {
		return
	}
	start := w.quotes[len(w.quotes)-1]
	w.quotes = w.quotes[:len(w.quotes)-1]
	out := w.b.String()
	quoted := "> " + strings.ReplaceAll(strings.Trim(out[start:], "\n"), "\n", "\n> ")
	w.b.Reset()
	w.b.WriteString(out[:start] + quoted + "\n")
}
