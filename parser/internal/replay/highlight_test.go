package replay

import "testing"

// Round where CT player 1 is left alone against 2 T and wins: a 2K + 1v2 clutch.
func clutchRound() *Round {
	ps := func(alive map[uint64]bool) []PlayerState {
		var out []PlayerState
		for id := uint64(1); id <= 10; id++ {
			out = append(out, PlayerState{ID: id, Name: "p", CT: id <= 5, Alive: alive[id]})
		}
		return out
	}
	all := map[uint64]bool{}
	for i := uint64(1); i <= 10; i++ {
		all[i] = true
	}
	return &Round{
		N: 7, StartTick: 1000, EndTick: 9000, CTWon: true,
		Frames: []Frame{{Tick: 1000, Players: ps(all)}},
		Kills: []Kill{
			{Tick: 2000, Killer: 6, Victim: 2, KillerCT: false, VictimCT: true},
			{Tick: 2100, Killer: 7, Victim: 3, KillerCT: false, VictimCT: true},
			{Tick: 2200, Killer: 8, Victim: 4, KillerCT: false, VictimCT: true},
			{Tick: 2300, Killer: 1, Victim: 8, KillerCT: true, VictimCT: false, KillerName: "hero", HS: true},
			{Tick: 2400, Killer: 9, Victim: 5, KillerCT: false, VictimCT: true}, // hero now alone vs 6, 7, 9, 10
			{Tick: 2500, Killer: 1, Victim: 6, KillerCT: true, VictimCT: false, KillerName: "hero"},
			{Tick: 2600, Killer: 1, Victim: 7, KillerCT: true, VictimCT: false, KillerName: "hero"},
			{Tick: 2700, Killer: 1, Victim: 9, KillerCT: true, VictimCT: false, KillerName: "hero"},
			{Tick: 2800, Killer: 1, Victim: 10, KillerCT: true, VictimCT: false, KillerName: "hero"},
		},
	}
}

func TestClutchAndAce(t *testing.T) {
	rec := &Recording{TickRate: 64, Rounds: []*Round{clutchRound()}}
	hs := Highlights(rec)
	if len(hs) == 0 || hs[0].SteamID != 1 {
		t.Fatalf("hero should be the top highlight, got %+v", hs)
	}
	h := hs[0]
	if h.Kills != 5 || h.ClutchVs != 4 || h.Label != "ACE + 1v4 clutch" {
		t.Fatalf("got kills=%d vs=%d label=%q", h.Kills, h.ClutchVs, h.Label)
	}
	if h.Score != 25+6*4+0.5 {
		t.Fatalf("score %.1f", h.Score)
	}
	// Clutch clips run to the round end, capped at 30 s.
	if h.ToTick != 9000 || h.ToTick-h.FromTick != 30*64 {
		t.Fatalf("window %d..%d", h.FromTick, h.ToTick)
	}
}

func TestLostClutchIsNotAClutch(t *testing.T) {
	r := clutchRound()
	r.CTWon = false
	r.Kills = r.Kills[:6] // hero kills two then the round is lost
	hs := Highlights(&Recording{TickRate: 64, Rounds: []*Round{r}})
	for _, h := range hs {
		if h.SteamID == 1 && h.ClutchVs != 0 {
			t.Fatalf("lost clutch counted: %+v", h)
		}
	}
}

func TestTeamKillsIgnored(t *testing.T) {
	r := clutchRound()
	r.Kills = []Kill{{Tick: 2000, Killer: 1, Victim: 2, KillerCT: true, VictimCT: true, KillerName: "oops"}}
	if hs := Highlights(&Recording{TickRate: 64, Rounds: []*Round{r}}); len(hs) != 0 {
		t.Fatalf("team kill produced a highlight: %+v", hs)
	}
}
