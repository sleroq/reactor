package main

import "testing"

func TestCommandName(t *testing.T) {
	tests := map[string]struct {
		text, username, want string
	}{
		"rating":             {text: "/r", want: "r"},
		"rating bot mention": {text: "/r@ReactorBot", username: "reactorbot", want: "r"},
		"help":               {text: "/help", want: "help"},
		"help bot mention":   {text: "/help@ReactorBot", username: "reactorbot", want: "help"},
		"other bot":          {text: "/help@OtherBot", username: "reactorbot"},
		"unknown":            {text: "/start", username: "reactorbot"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := commandName(test.text, test.username); got != test.want {
				t.Errorf("commandName(%q, %q) = %q, want %q", test.text, test.username, got, test.want)
			}
		})
	}
}
