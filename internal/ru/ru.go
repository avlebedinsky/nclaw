// Package ru formats dates, durations and counts for the bot's Russian messages.
package ru

import (
	"fmt"
	"strings"
	"time"
)

var (
	weekdays = [...]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	months   = [...]string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
)

// Date formats t as "пн 5 окт 15:04".
func Date(t time.Time) string {
	return fmt.Sprintf("%s %d %s %s", weekdays[t.Weekday()], t.Day(), months[t.Month()-1], t.Format("15:04"))
}

// Plural picks the form of a noun that agrees with n: one ("1 день"), few ("2 дня") or many ("5 дней").
func Plural(n int, one, few, many string) string {
	n10, n100 := n%10, n%100
	switch {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	default:
		return many
	}
}

// Duration formats d rounded to the second as "1 ч 5 мин", "2 мин 30 с" or "12 с".
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%d ч", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%d мин", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d с", s))
	}
	return strings.Join(parts, " ")
}
