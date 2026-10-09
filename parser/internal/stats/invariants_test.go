package stats

import "testing"

func TestInvariantsCatchWhatTheCountersShare(t *testing.T) {
	players := []RoundPlayer{{SteamID: "a", Side: "CT"}, {SteamID: "x", Side: "T", HP: 40}}
	ok := Round{Winner: "CT", Players: players, Kills: []Kill{{Killer: "x", Victim: "a", Weapon: "AK-47"}}}
	if b := checkInvariants([]Round{ok}, []Team{{Score: 1, GameScore: 1}}); len(b) != 0 {
		t.Fatalf("a clean round broke %+v", b)
	}

	// A replayed round that was not voided: every hit twice (Spirit-MOUZ Ancient r13 took 200).
	replayed := ok
	replayed.Damage = []Damage{{Victim: "a", HP: 100}, {Victim: "a", HP: 100}}
	// A bot's removal logged as a death: victim "0" never played the round.
	phantom := ok
	phantom.Kills = append([]Kill{{Victim: "0", Weapon: "World"}}, ok.Kills...)
	// Two ordinary deaths: impossible without a bot takeover, which starts with a "World" death.
	twice := ok
	twice.Kills = append(ok.Kills, Kill{Killer: "x", Victim: "a", Weapon: "AK-47"})
	takeover := ok
	takeover.Kills = append([]Kill{{Victim: "a", Weapon: "World"}}, ok.Kills...)

	for name, tc := range map[string]struct {
		r    Round
		rule string
	}{
		"replayed": {replayed, "at most 100 HP taken"},
		"phantom":  {phantom, "the dead played the round"},
		"twice":    {twice, "one death a round"},
	} {
		b := checkInvariants([]Round{tc.r}, nil)
		if len(b) != 1 || b[0].Rule != tc.rule {
			t.Errorf("%s: got %+v, want %q", name, b, tc.rule)
		}
	}
	if b := checkInvariants([]Round{takeover}, nil); len(b) != 0 {
		t.Errorf("a bot takeover's second life broke %+v", b)
	}
	// Out twice in freeze time and back each time, then killed in play (FRAG Midwest 3627 r5).
	rejoined := ok
	rejoined.Kills = append([]Kill{{Victim: "a", Weapon: "World"}, {Victim: "a", Weapon: "World"}}, ok.Kills...)
	if b := checkInvariants([]Round{rejoined}, nil); len(b) != 0 {
		t.Errorf("rejoining after leaving broke %+v", b)
	}
	if b := checkInvariants(nil, []Team{{Name: "MOUZ", Score: 12, GameScore: 13}}); len(b) != 1 {
		t.Errorf("a lost round passed: %+v", b)
	}
}
