package openai

import (
	"strings"
	"testing"
)

func TestBuildOnboardingPrompt(t *testing.T) {
	ctx := OnboardingContext{
		PublicURL: "https://example.com",
		Bots: []BotInfo{
			{Name: "test-bot", IsAlive: true, Specializations: []string{"python", "rust"}},
		},
		Projects: []ProjectInfo{
			{Name: "demo", Description: "A demo project"},
		},
	}

	prompt := BuildOnboardingPrompt(ctx)

	checks := []string{
		"agenthub assistant",
		"How to install",
		"/agenthub bind",
		"https://example.com",
		"test-bot",
		"python, rust",
		"demo",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing %q", check)
		}
	}
}

func TestBuildOnboardingPromptEmpty(t *testing.T) {
	prompt := BuildOnboardingPrompt(OnboardingContext{})
	if !strings.Contains(prompt, "No bots are currently registered") {
		t.Error("expected empty-bots message")
	}
}
