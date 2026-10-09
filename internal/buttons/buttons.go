// Package buttons defines the inline buttons nclaw puts under its messages and the
// callback data Telegram sends back when one of them is pressed.
package buttons

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
)

// Button is one inline button: its label and the data returned when it is pressed.
type Button struct {
	Text string
	Data string
}

// Keyboard is the rows of buttons under a message.
type Keyboard [][]Button

// Kind tells what a pressed button asks for.
type Kind int

// Button kinds.
const (
	Unknown Kind = iota
	Choice
	Snooze
	Done
	Stop
	AnswerNew
	TaskAction
)

// Task actions carried by TaskAction buttons.
const (
	TaskPause  = "pause"
	TaskResume = "resume"
	TaskCancel = "cancel"
)

// Press is the decoded data of a pressed button.
type Press struct {
	Kind   Kind
	Index  int
	After  time.Duration
	Action string
	TaskID string
}

const (
	shortLabel   = 20
	choicesInRow = 2
)

var (
	snoozes     = []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour}
	snoozeLabel = map[time.Duration]string{15 * time.Minute: "+15 мин", time.Hour: "+1 час", 24 * time.Hour: "Завтра"}
	taskCodes   = map[string]string{TaskPause: "p", TaskResume: "r", TaskCancel: "c"}
)

// Choices lays out answer options: two per row when every label is short, else one per row.
func Choices(labels []string) Keyboard {
	perRow := choicesInRow
	for _, l := range labels {
		if utf8.RuneCountInString(l) > shortLabel {
			perRow = 1
		}
	}
	var kb Keyboard
	for i, l := range labels {
		b := Button{Text: l, Data: "c:" + strconv.Itoa(i)}
		if i%perRow == 0 {
			kb = append(kb, []Button{b})
			continue
		}
		kb[len(kb)-1] = append(kb[len(kb)-1], b)
	}
	return kb
}

// Reminder returns the buttons of a reminder: done, and snooze for 15 minutes, an hour or a day.
func Reminder() Keyboard {
	row := make([]Button, 0, 1+len(snoozes))
	row = append(row, Button{Text: "✅ Готово", Data: "d"})
	for _, d := range snoozes {
		row = append(row, Button{Text: snoozeLabel[d], Data: fmt.Sprintf("z:%d", int(d.Minutes()))})
	}
	return Keyboard{row}
}

// Progress returns the buttons of a run's status message; pending is the number of
// messages that arrived while the run was going on.
func Progress(pending int) Keyboard {
	row := []Button{{Text: "⏹ Стоп", Data: "s"}}
	if pending > 0 {
		row = append(row, Button{Text: fmt.Sprintf("⏭ Ответить на новые (%d)", pending), Data: "n"})
	}
	return Keyboard{row}
}

// Task returns the row of buttons for the n-th listed task: pause or resume, and cancel.
func Task(n int, taskID string, paused bool) []Button {
	toggle := Button{Text: fmt.Sprintf("⏸ %d", n), Data: "t:p:" + taskID}
	if paused {
		toggle = Button{Text: fmt.Sprintf("▶️ %d", n), Data: "t:r:" + taskID}
	}
	return []Button{toggle, {Text: fmt.Sprintf("🗑 %d", n), Data: "t:c:" + taskID}}
}

// Markup converts kb to a Telegram inline keyboard; an empty kb removes the keyboard of an edited message.
func Markup(kb Keyboard) models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(kb))
	for _, row := range kb {
		r := make([]models.InlineKeyboardButton, 0, len(row))
		for _, b := range row {
			r = append(r, models.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data})
		}
		rows = append(rows, r)
	}
	return models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// FromMarkup converts a message's inline keyboard back to a Keyboard.
func FromMarkup(m *models.InlineKeyboardMarkup) Keyboard {
	if m == nil {
		return nil
	}
	kb := make(Keyboard, 0, len(m.InlineKeyboard))
	for _, row := range m.InlineKeyboard {
		r := make([]Button, 0, len(row))
		for i := range row {
			r = append(r, Button{Text: row[i].Text, Data: row[i].CallbackData})
		}
		kb = append(kb, r)
	}
	return kb
}

// Label returns the label of the button in kb that carries data, or "".
func (kb Keyboard) Label(data string) string {
	for _, row := range kb {
		for _, b := range row {
			if b.Data == data {
				return b.Text
			}
		}
	}
	return ""
}

// Parse decodes the data of a pressed button; unknown data gives Kind Unknown.
func Parse(data string) Press {
	head, rest, _ := strings.Cut(data, ":")
	switch head {
	case "c":
		return parseChoice(rest)
	case "z":
		return parseSnooze(rest)
	case "d":
		return Press{Kind: Done}
	case "s":
		return Press{Kind: Stop}
	case "n":
		return Press{Kind: AnswerNew}
	case "t":
		return parseTask(rest)
	default:
		return Press{}
	}
}

func parseChoice(s string) Press {
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 {
		return Press{}
	}
	return Press{Kind: Choice, Index: i}
}

func parseSnooze(s string) Press {
	minutes, err := strconv.Atoi(s)
	if err != nil {
		return Press{}
	}
	after := time.Duration(minutes) * time.Minute
	if _, ok := snoozeLabel[after]; !ok {
		return Press{}
	}
	return Press{Kind: Snooze, After: after}
}

func parseTask(s string) Press {
	code, id, ok := strings.Cut(s, ":")
	if !ok || id == "" {
		return Press{}
	}
	for action, c := range taskCodes {
		if c == code {
			return Press{Kind: TaskAction, Action: action, TaskID: id}
		}
	}
	return Press{}
}
