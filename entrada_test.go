package main

import (
	"testing"
	"time"
)

func TestQuando(t *testing.T) {
	agora := time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)
	casos := []struct {
		t    time.Time
		want string
	}{
		{agora.Add(-30 * time.Second), "agora"},
		{agora.Add(-5 * time.Minute), "há 5 min"},
		{agora.Add(-3 * time.Hour), "há 3 h"},
		{time.Date(2026, 11, 2, 23, 0, 0, 0, time.Local), "ontem"},
		{time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local), "12 out"},
		{time.Date(2025, 10, 12, 9, 0, 0, 0, time.Local), "12 out 2025"},
	}
	for _, c := range casos {
		if got := quando(c.t, agora); got != c.want {
			t.Errorf("quando(%v) = %q, quero %q", c.t, got, c.want)
		}
	}
}

func TestQuemEsta(t *testing.T) {
	casos := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"ana"}, "Ana está aqui"},
		{[]string{"ana", "bia"}, "Ana e Bia estão aqui"},
		{[]string{"ana", "bia", "caio"}, "3 pessoas aqui"},
	}
	for _, c := range casos {
		if got := quemEsta(c.in); got != c.want {
			t.Errorf("quemEsta(%v) = %q, quero %q", c.in, got, c.want)
		}
	}
}
