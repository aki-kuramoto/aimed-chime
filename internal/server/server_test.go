package server

import (
	"strings"
	"testing"

	"github.com/aki-kuramoto/aimed-chime/internal/sound"
)

func TestUserInstructions(t *testing.T) {
	if s := userInstructions(nil); s != "" {
		t.Errorf("no instructions: %q", s)
	}
	s := userInstructions([]string{"chime once, then speak", "say which session is calling"})
	if !strings.Contains(s, "\n- chime once, then speak\n- say which session is calling") {
		t.Errorf("instructions: %q", s)
	}
	l := sound.Listing{ConfigPath: "config.setay", Config: sound.Config{Instructions: []string{"say which session is calling"}}}
	if out := ListText(l); !strings.HasSuffix(out, "- say which session is calling\n") {
		t.Errorf("listing: %q", out)
	}
}
