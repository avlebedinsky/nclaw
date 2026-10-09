package buttons

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := map[string]Press{
		"c:2":         {Kind: Choice, Index: 2},
		"z:15":        {Kind: Snooze, After: 15 * time.Minute},
		"z:1440":      {Kind: Snooze, After: 24 * time.Hour},
		"d":           {Kind: Done},
		"s":           {Kind: Stop},
		"n":           {Kind: AnswerNew},
		"t:p:task-1a": {Kind: TaskAction, Action: TaskPause, TaskID: "task-1a"},
		"t:r:task-1a": {Kind: TaskAction, Action: TaskResume, TaskID: "task-1a"},
		"t:c:task-1a": {Kind: TaskAction, Action: TaskCancel, TaskID: "task-1a"},
		"forget":      {Kind: ForgetMemory},
		"compact":     {Kind: Compact},
		"reset":       {Kind: NewConversation},
	}
	for data, want := range cases {
		assert.Equal(t, want, Parse(data), data)
	}
	for _, bad := range []string{"", "x", "c:", "c:-1", "z:7", "z:abc", "t:x:id", "t:p:", "t:p"} {
		assert.Equal(t, Unknown, Parse(bad).Kind, bad)
	}
}

func TestChoices_TwoPerRowWhenShort(t *testing.T) {
	kb := Choices([]string{"Да", "Нет", "Позже"})

	require.Len(t, kb, 2)
	assert.Equal(t, []Button{{Text: "Да", Data: "c:0"}, {Text: "Нет", Data: "c:1"}}, kb[0])
	assert.Equal(t, []Button{{Text: "Позже", Data: "c:2"}}, kb[1])
}

func TestChoices_OnePerRowWhenAnyIsLong(t *testing.T) {
	kb := Choices([]string{"Да", "Удалить всё и начать заново"})

	require.Len(t, kb, 2)
	assert.Len(t, kb[0], 1)
	assert.Len(t, kb[1], 1)
}

func TestReminderButtonsDecodeToTheirActions(t *testing.T) {
	kb := Reminder()

	require.Len(t, kb, 1)
	labels := []string{"✅ Готово", "+15 мин", "+1 час", "Завтра"}
	kinds := []Kind{Done, Snooze, Snooze, Snooze}
	for i, b := range kb[0] {
		assert.Equal(t, labels[i], b.Text)
		assert.Equal(t, kinds[i], Parse(b.Data).Kind)
	}
	assert.Equal(t, 24*time.Hour, Parse(kb[0][3].Data).After)
}

func TestProgress_AnswerNewOnlyWithPendingMessages(t *testing.T) {
	assert.Equal(t, Keyboard{{{Text: "⏹ Стоп", Data: "s"}}}, Progress(0))
	assert.Equal(t, Keyboard{{{Text: "⏹ Стоп", Data: "s"}, {Text: "⏭ Ответить на новые (2)", Data: "n"}}}, Progress(2))
}

func TestTask(t *testing.T) {
	id := "task-1791062299007-6cb1cdf7"
	assert.Equal(t, []Button{{Text: "⏸ 1", Data: "t:p:" + id}, {Text: "🗑 1", Data: "t:c:" + id}}, Task(1, id, false))
	assert.Equal(t, "▶️ 2", Task(2, id, true)[0].Text)
	for _, b := range Task(1, id, true) {
		assert.LessOrEqual(t, len(b.Data), 64)
	}
}

func TestMarkupRoundTripAndLabel(t *testing.T) {
	kb := Choices([]string{"Да", "Нет"})

	m := Markup(kb)
	back := FromMarkup(&m)

	assert.Equal(t, kb, back)
	assert.Equal(t, "Нет", back.Label("c:1"))
	assert.Empty(t, back.Label("c:5"))
	assert.Nil(t, FromMarkup(nil))
	assert.Empty(t, Markup(nil).InlineKeyboard)
}

func TestModels_TicksTheCurrentChoiceTwoPerRow(t *testing.T) {
	kb := Models([]string{"opus", "sonnet", "haiku"}, "sonnet")

	assert.Equal(t, Keyboard{
		{{Text: "Opus", Data: "m:opus"}, {Text: "✓ Sonnet", Data: "m:sonnet"}},
		{{Text: "Haiku", Data: "m:haiku"}, {Text: "По умолчанию", Data: "m:"}},
	}, kb)
	assert.Equal(t, "✓ По умолчанию", Models([]string{"opus"}, "")[0][1].Text)
	assert.Equal(t, Press{Kind: Model, Model: "opus"}, Parse("m:opus"))
	assert.Equal(t, Press{Kind: Model}, Parse("m:"))
	assert.Equal(t, "по умолчанию", ModelName(""))
}
