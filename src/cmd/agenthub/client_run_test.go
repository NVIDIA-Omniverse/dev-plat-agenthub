package main

import (
	"testing"
)

func TestShouldEscalate(t *testing.T) {
	tests := []struct {
		reply   string
		escalate bool
	}{
		{"I'm not sure about that.", true},
		{"I don't know the answer.", true},
		{"I cannot help with that.", true},
		{"I'm unable to assist.", true},
		{"This is beyond my capabilities.", true},
		{"I'd recommend asking a specialist.", true},
		{"I'm not confident in my answer.", true},
		{"Here is the answer: 42.", false},
		{"Sure, I can help with that.", false},
		{"", false},
	}
	for _, tt := range tests {
		got := shouldEscalate(tt.reply)
		if got != tt.escalate {
			t.Errorf("shouldEscalate(%q) = %v, want %v", tt.reply, got, tt.escalate)
		}
	}
}
