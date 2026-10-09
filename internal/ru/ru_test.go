package ru

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDate(t *testing.T) {
	assert.Equal(t, "пн 5 окт 09:00", Date(time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)))
	assert.Equal(t, "вс 3 мая 23:59", Date(time.Date(2026, 5, 3, 23, 59, 0, 0, time.UTC)))
}

func TestPlural(t *testing.T) {
	cases := map[int]string{1: "день", 2: "дня", 4: "дня", 5: "дней", 11: "дней", 12: "дней", 14: "дней", 21: "день", 22: "дня", 25: "дней", 0: "дней"}
	for n, want := range cases {
		assert.Equal(t, want, Plural(n, "день", "дня", "дней"), n)
	}
}

func TestDuration(t *testing.T) {
	assert.Equal(t, "12 с", Duration(12*time.Second))
	assert.Equal(t, "1 мин 30 с", Duration(90*time.Second))
	assert.Equal(t, "1 ч 5 мин", Duration(time.Hour+5*time.Minute))
	assert.Equal(t, "2 ч", Duration(2*time.Hour+400*time.Millisecond))
	assert.Equal(t, "0 с", Duration(0))
}
