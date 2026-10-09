package stats

import (
	"testing"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

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

// played rounds collected so far, with a snapshot after each, as onRoundEnd would leave them.
func collectedRounds(n int) *Collector {
	c := &Collector{players: map[uint64]*Player{7: {SteamID: "7"}}}
	for i := 1; i <= n; i++ {
		c.players[7].K += 2 // two kills a round, so totals identify the round they belong to
		c.players[7].kastRounds++
		c.rounds = append(c.rounds, Round{N: i})
		c.snapshot()
	}
	return c
}

// A knife round then a restart: the game is back at 0-0, so the knife round must go (as before).
func TestRestartAtZeroZeroDiscardsTheKnifeRound(t *testing.T) {
	c := collectedRounds(1)
	c.onMatchStart(events.MatchStart{})
	c.settleRestart(0)
	if len(c.rounds) != 0 || len(c.players) != 0 || c.restarts != 1 || c.restores != 0 {
		t.Fatalf("knife round survived: rounds=%d players=%d restarts=%d restores=%d", len(c.rounds), len(c.players), c.restarts, c.restores)
	}
}

// A LAN match medic: 13 rounds collected, then a backup of round 12 is restored (the game says 12
// rounds played). Rounds 1-12 must survive with their stats; round 13 is replayed, so it goes.
func TestRoundBackupRestoreKeepsTheRoundsBeforeIt(t *testing.T) {
	c := collectedRounds(13)
	c.onMatchStart(events.MatchStart{})
	c.settleRestart(12)
	if len(c.rounds) != 12 {
		t.Fatalf("rounds = %d, want 12 (a medic restore must not wipe the match)", len(c.rounds))
	}
	if k := c.players[7].K; k != 24 {
		t.Fatalf("kills = %d, want 24 (totals as of round 12)", k)
	}
	if c.restores != 1 || c.restarts != 0 {
		t.Fatalf("restores=%d restarts=%d, want 1 and 0", c.restores, c.restarts)
	}
	// Play continues from the restored state, and later rounds must not write into the snapshot.
	c.players[7].K += 5
	c.rounds = append(c.rounds, Round{N: 13})
	c.snapshot()
	if c.snapshots[11][0].K != 24 {
		t.Fatalf("snapshot 12 was changed by a later round: K=%d", c.snapshots[11][0].K)
	}
}

// A MatchStart that rewinds nothing (the game has played at least as many rounds as we hold).
func TestMatchStartWithNothingReplayedKeepsEverything(t *testing.T) {
	c := collectedRounds(5)
	c.onMatchStart(events.MatchStart{})
	c.settleRestart(5)
	if len(c.rounds) != 5 || c.players[7].K != 10 || c.restarts != 0 || c.restores != 0 {
		t.Fatalf("lost data: rounds=%d K=%d restarts=%d restores=%d", len(c.rounds), c.players[7].K, c.restarts, c.restores)
	}
}

// LO3 fires MatchStart three times; only the settled score matters, and it is judged once.
func TestRepeatedMatchStartsAreJudgedOnce(t *testing.T) {
	c := collectedRounds(1)
	for i := 0; i < 3; i++ {
		c.onMatchStart(events.MatchStart{})
	}
	c.settleRestart(0)
	c.settleRestart(0) // no pending restart any more: must be a no-op
	if c.restarts != 1 || len(c.rounds) != 0 {
		t.Fatalf("restarts=%d rounds=%d, want 1 and 0", c.restarts, len(c.rounds))
	}
}

// The round being played when the MatchStart fires is abandoned: it is replayed either way.
func TestMatchStartDropsTheRoundInProgress(t *testing.T) {
	c := collectedRounds(3)
	c.cur = &Round{N: 4}
	c.onMatchStart(events.MatchStart{})
	if c.cur != nil {
		t.Fatal("the interrupted round survived the MatchStart")
	}
}

// A replayed round (FRAG Midwest 3563: 12-3 became 11-4 in the next recording segment) replaces the
// original: only what the replay itself added is kept, on top of the totals before the original.
func TestReplayedRoundKeepsOnlyTheReplaysOwnStats(t *testing.T) {
	c := collectedRounds(3) // player 7 has 2 kills per round: 6 after round 3
	c.players[7].K += 5     // the replay of round 3, in progress: 5 kills
	delta := c.roundDelta()
	c.rewind(2)
	c.addDelta(delta)
	if got := c.players[7].K; got != 4+5 {
		t.Fatalf("kills after replaying round 3 = %d, want 9 (rounds 1-2) + 5 (replay)", got)
	}
	if len(c.rounds) != 2 {
		t.Fatalf("rounds = %d, want 2 before the replay is appended", len(c.rounds))
	}
}
