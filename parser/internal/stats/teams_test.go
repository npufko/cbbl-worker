package stats

import "testing"

var (
	teamA = []string{"a1", "a2", "a3", "a4", "a5"}
	teamB = []string{"b1", "b2", "b3", "b4", "b5"}
)

// Teams are followed by who is on them: sides swap at half time, the team does not.
func TestTeamsFollowPlayersAcrossTheSwap(t *testing.T) {
	c := &Collector{}
	if got := c.assignTeams(teamA, teamB); got != 0 {
		t.Fatalf("round 1: CT team = %d, want 0 (the first CT side founds team 0)", got)
	}
	if got := c.assignTeams(teamB, teamA); got != 1 {
		t.Fatalf("after the swap: CT team = %d, want 1", got)
	}
	// A sub replaces a5 on team A, now on T; a majority is enough to keep the team.
	if got := c.assignTeams(teamB, []string{"a1", "a2", "a3", "a4", "a6"}); got != 1 {
		t.Fatalf("with a sub: CT team = %d, want 1", got)
	}
	if !c.teams[0].players["a6"] || c.teams[1].players["a6"] {
		t.Fatal("the sub was added to the wrong team")
	}
}

// A team whose players all disconnect before the end still counts through the other side.
func TestTeamsResolvedFromTheOtherSideAlone(t *testing.T) {
	c := &Collector{}
	c.assignTeams(teamA, teamB)
	if got := c.assignTeams(teamB, nil); got != 1 {
		t.Fatalf("CT team = %d, want 1 (T side empty)", got)
	}
}

// Score is counted from the kept rounds: a team wins a round when it is on the winning side.
func TestTeamScoresCountRoundsWonBySide(t *testing.T) {
	c := &Collector{}
	c.assignTeams(teamA, teamB)
	c.rounds = []Round{
		{N: 1, Winner: "CT", CTTeam: 0}, // A (CT) wins
		{N: 2, Winner: "T", CTTeam: 0},  // B (T) wins
		{N: 3, Winner: "CT", CTTeam: 1}, // after the swap: B (CT) wins
		{N: 4, Winner: "T", CTTeam: 1},  // A (T) wins
		{N: 5, Winner: "T", CTTeam: 1},  // A (T) wins
	}
	teams := c.teamResults(func(i int) int { return 99 })
	if teams[0].Score != 3 || teams[1].Score != 2 {
		t.Fatalf("scores = %d-%d, want 3-2", teams[0].Score, teams[1].Score)
	}
	if len(teams[0].Players) != 5 || teams[0].Players[0] != "a1" {
		t.Fatalf("team 0 players = %v", teams[0].Players)
	}
}
