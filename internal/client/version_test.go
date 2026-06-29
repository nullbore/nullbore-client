package client

import "testing"

func TestAPICompatWarning(t *testing.T) {
	cases := []struct {
		server string
		warn   bool
	}{
		{"", false},                  // no header → unknown, don't warn
		{SupportedAPIVersion, false}, // exact match
		{"1", false},                 // equal
		{"0", false},                 // server older than client → no warn
		{"2", true},                  // server newer → warn to upgrade
		{"3", true},
		{"garbage", false}, // unparseable → never warn spuriously
		{"1.2", false},     // not an integer major → no warn
	}
	for _, c := range cases {
		if got := apiCompatWarning(c.server) != ""; got != c.warn {
			t.Errorf("apiCompatWarning(%q): warn=%v, want %v", c.server, got, c.warn)
		}
	}
}
