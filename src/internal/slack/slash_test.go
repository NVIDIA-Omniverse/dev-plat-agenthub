package slack

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBindValid(t *testing.T) {
	cmd, err := ParseBind("bind 1.2.3.4:8080 mybot")
	require.NoError(t, err)
	require.Equal(t, "1.2.3.4", cmd.Host)
	require.Equal(t, 8080, cmd.Port)
	require.Equal(t, "mybot", cmd.Name)
}

func TestParseBindIPv6(t *testing.T) {
	cmd, err := ParseBind("bind ::1:9090 bot-two")
	require.NoError(t, err)
	require.Equal(t, "::1", cmd.Host)
	require.Equal(t, 9090, cmd.Port)
}

func TestParseBindInvalidName(t *testing.T) {
	_, err := ParseBind("bind localhost:8080 MyBOT")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unique-name")
}

func TestParseBindMissingPort(t *testing.T) {
	_, err := ParseBind("bind localhost mybot")
	require.Error(t, err)
	require.Contains(t, err.Error(), "port")
}

func TestParseBindBadPort(t *testing.T) {
	_, err := ParseBind("bind localhost:99999 mybot")
	require.Error(t, err)
}

func TestParseBindWrongSubcommand(t *testing.T) {
	_, err := ParseBind("list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage")
}

func TestParseBindMissingHostPort(t *testing.T) {
	_, err := ParseBind("bind")
	require.Error(t, err)
}

func TestParseTaskWithBot(t *testing.T) {
	tc := ParseTask("fix the login bug @mybot")
	require.Equal(t, "fix the login bug", tc.Description)
	require.Equal(t, "mybot", tc.BotName)
}

func TestParseTaskWithoutBot(t *testing.T) {
	tc := ParseTask("add dark mode support")
	require.Equal(t, "add dark mode support", tc.Description)
	require.Equal(t, "", tc.BotName)
}

func TestParseTaskEmpty(t *testing.T) {
	tc := ParseTask("")
	require.Equal(t, "", tc.Description)
	require.Equal(t, "", tc.BotName)
}

func TestParseTaskOnlyBot(t *testing.T) {
	tc := ParseTask("@mybot")
	// When the only word is a @mention, description is empty.
	require.Equal(t, "", tc.Description)
	require.Equal(t, "mybot", tc.BotName)
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"bind 1.2.3.4:8080 mybot", "bind"},
		{"list", "list"},
		{"remove mybot", "remove"},
		{"chatty mybot", "chatty"},
		{"fix the login bug", "task"},
		{"", "task"},
		{"  fix bug @bot", "task"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.expected, ParseCommand(tt.input))
		})
	}
}

func TestFormatBotListEmpty(t *testing.T) {
	msg := FormatBotList(nil)
	require.Contains(t, msg, "No bots")
}

func TestFormatBotListWithBots(t *testing.T) {
	bots := []BotSummary{
		{Name: "bot1", Host: "1.2.3.4", Port: 8080, IsAlive: true},
		{Name: "bot2", Host: "5.6.7.8", Port: 9090, IsAlive: false, Chatty: true},
	}
	msg := FormatBotList(bots)
	require.Contains(t, msg, "bot1")
	require.Contains(t, msg, "online")
	require.Contains(t, msg, "bot2")
	require.Contains(t, msg, "offline")
	require.Contains(t, msg, "chatty")
}

func TestStripMention(t *testing.T) {
	require.Equal(t, "hello world", stripMention("<@U123456> hello world"))
	require.Equal(t, "no mention here", stripMention("no mention here"))
	require.Equal(t, "", stripMention("<@U123456>"))
}

func TestSplitArgs(t *testing.T) {
	parts := splitArgs("remove mybot extra", 2)
	require.Equal(t, []string{"remove", "mybot extra"}, parts)
}

func TestParseHostPort(t *testing.T) {
	host, port, err := parseHostPort("localhost:8080")
	require.NoError(t, err)
	require.Equal(t, "localhost", host)
	require.Equal(t, 8080, port)
}

func TestParseHostPortMissingColon(t *testing.T) {
	_, _, err := parseHostPort("localhost")
	require.Error(t, err)
}

func TestParseHostPortInvalidPort(t *testing.T) {
	_, _, err := parseHostPort("localhost:abc")
	require.Error(t, err)
}

func TestParseHostPortZeroPort(t *testing.T) {
	_, _, err := parseHostPort("localhost:0")
	require.Error(t, err)
}

func TestParseHostPortMissingHost(t *testing.T) {
	_, _, err := parseHostPort(":8080")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing host")
}
