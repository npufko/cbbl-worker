package stats

import "testing"

// A real ESEA Bo3 map (de_ancient, official score 2-13 = 15 rounds) parsed as 16: round 1 was the
// knife round, equipment [200,0] against the real pistol round's [1000,1000], and its 7 kills were
// being counted towards every player's K/D, ADR and rating. FACEIT restarts the game after the
// knife round, so MatchStart fires again and everything before it must be dropped.
func TestResetDiscardsEverythingBeforeTheRestart(t *testing.T) {
	c := &Collector{
		rounds:  []Round{{N: 1, Winner: "CT", Reason: 8, Equip: [2]int{200, 0}}},
		players: map[uint64]*Player{7: {SteamID: "7", K: 4, D: 1, Damage: 300, kastRounds: 1}},
		cur:     &Round{N: 2},
	}
	c.reset()

	if len(c.rounds) != 0 {
		t.Fatalf("knife round survived the restart: %d rounds left", len(c.rounds))
	}
	if len(c.players) != 0 {
		t.Fatalf("knife-round kills survived the restart: %d players left", len(c.players))
	}
	if c.cur != nil {
		t.Fatal("a round in progress survived the restart")
	}
	if c.restarts != 1 {
		t.Fatalf("restarts = %d, want 1", c.restarts)
	}
}

// LO3 fires MatchStart several times in a row; resetting has to stay safe when there is nothing yet.
func TestResetIsSafeWhenNothingCollectedYet(t *testing.T) {
	c := &Collector{players: map[uint64]*Player{}}
	c.reset()
	c.reset()
	c.reset()
	if c.restarts != 3 || len(c.rounds) != 0 || len(c.players) != 0 {
		t.Fatalf("repeated resets misbehaved: restarts=%d rounds=%d players=%d", c.restarts, len(c.rounds), len(c.players))
	}
	// The per-round maps must be usable, not nil, so the next round can record into them.
	if c.contributed == nil || c.died == nil || c.killerOf == nil || c.clutch == nil {
		t.Fatal("reset left per-round state nil")
	}
}
