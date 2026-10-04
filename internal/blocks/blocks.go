// Package blocks defines the fenced command blocks a CLI reply can carry.
package blocks

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Command block patterns; the first submatch of each is the block's JSON body.
var (
	Schedule = regexp.MustCompile("(?s)```nclaw:schedule\n(.*?)\n```")
	Webhook  = regexp.MustCompile("(?s)```nclaw:webhook\n(.*?)\n```")
	SendFile = regexp.MustCompile("(?s)```nclaw:sendfile\n(.*?)\n```")
	Buttons  = regexp.MustCompile("(?s)```nclaw:buttons\n(.*?)\n```")
)

const (
	maxButtons     = 6
	maxButtonRunes = 40
	openFence      = "```nclaw:"
)

// StripAll removes every known command block from text and trims the result.
func StripAll(text string) string {
	for _, re := range []*regexp.Regexp{SendFile, Schedule, Webhook, Buttons} {
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

// StripPartial is StripAll for text still being written: it also drops a command
// block that has been opened but not closed yet.
func StripPartial(text string) string {
	text = StripAll(text)
	if i := strings.LastIndex(text, openFence); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// ButtonLabels returns the labels of the last nclaw:buttons block in text: non-empty,
// at most six, each cut to 40 characters. A malformed block gives none.
func ButtonLabels(text string) []string {
	matches := Buttons.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(matches[len(matches)-1][1]), &raw); err != nil {
		return nil
	}
	labels := make([]string, 0, len(raw))
	for _, l := range raw {
		if l = strings.TrimSpace(l); l != "" && len(labels) < maxButtons {
			labels = append(labels, cut(l, maxButtonRunes))
		}
	}
	return labels
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
