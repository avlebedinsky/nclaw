// Package blocks defines the fenced command blocks a CLI reply can carry.
package blocks

import (
	"regexp"
	"strings"
)

// Command block patterns; the first submatch of each is the block's JSON body.
var (
	Schedule = regexp.MustCompile("(?s)```nclaw:schedule\n(.*?)\n```")
	Webhook  = regexp.MustCompile("(?s)```nclaw:webhook\n(.*?)\n```")
	SendFile = regexp.MustCompile("(?s)```nclaw:sendfile\n(.*?)\n```")
)

// StripAll removes every known command block from text and trims the result.
func StripAll(text string) string {
	for _, re := range []*regexp.Regexp{SendFile, Schedule, Webhook} {
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}
