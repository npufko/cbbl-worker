package stats

import "testing"

// One round of a real FACEIT map, reduced: what the game's counters showed for it is the answer key.
func checkRound() Round {
	return Round{
		Players: []RoundPlayer{{SteamID: "a", Side: "CT"}, {SteamID: "b", Side: "CT"}, {SteamID: "x", Side: "T"}, {SteamID: "y", Side: "T"}, {SteamID: "z", Side: "T"}},
		Kills: []Kill{
			{Killer: "a", Victim: "x", Weapon: "USP-S", HS: true, Assister: "b"},
			{Killer: "a", Victim: "y", Weapon: "USP-S"},
			{Killer: "a", Victim: "z", Weapon: "Karambit"},
			{Killer: "b", Victim: "a", Weapon: "USP-S"},     // team kill: a death, no kill
			{Victim: "z", Weapon: "Molotov", Assister: "a"}, // z burned themself after a hit from a: a's assist
		},
		Damage: []Damage{
			{Attacker: "a", Victim: "x", HP: 100, Weapon: "USP-S"},
			{Attacker: "b", Victim: "y", HP: 1, Weapon: "HE Grenade", Impact: true}, // the grenade hit y in flight
			{Attacker: "b", Victim: "y", HP: 44, Weapon: "HE Grenade"},
			{Attacker: "b", Victim: "a", HP: 100, Weapon: "USP-S"}, // team damage
		},
		MVP: "a",
	}
}

// The game counts a grenade striking someone in flight as damage, not as utility damage
// (hotzzinho_-'s 1 + 44 HE on Moopy read m_iUtilityDamage 44), and a team kill as neither a kill
// nor anything but the victim's death.
func TestLogTotalsCountAsTheGameDoes(t *testing.T) {
	got := logTotals([]Round{checkRound()})
	want := map[string]map[string]int{
		"a": {"kills": 3, "hsKills": 1, "3k": 1, "knifeKills": 1, "damage": 100, "mvps": 1, "deaths": 1, "assists": 1},
		"b": {"assists": 1, "damage": 45, "utilDamage": 44},
	}
	for id, stats := range want {
		for stat, n := range stats {
			if got[id][stat] != n {
				t.Errorf("%s %s = %d, want %d", id, stat, got[id][stat], n)
			}
		}
	}
	if got["b"]["kills"] != 0 {
		t.Errorf("a team kill counted as a kill: %d", got["b"]["kills"])
	}
}

// A difference cbbl makes on purpose is reported with its reason and does not fail the check;
// any other difference in a scored stat does.
func TestCheckResultKnownDifferences(t *testing.T) {
	c := &Collector{check: newCheckState()}
	c.check.checks = 1
	c.check.last["p"] = reading{name: "stormie", game: map[string]int{"assists": 6, "kills": 10}, ours: map[string]int{"assists": 5, "kills": 10}}
	c.check.known[[2]string{"assists", "p"}] = 1
	c.check.knownWhy[[2]string{"assists", "p"}] = knownSlayAssist

	res := c.checkResult()
	if !res.OK || len(res.Diffs) != 1 || res.Diffs[0].Known != knownSlayAssist {
		t.Fatalf("known slay assist: ok=%v diffs=%+v", res.OK, res.Diffs)
	}

	c.check.last["p"].game["kills"] = 11
	if res := c.checkResult(); res.OK {
		t.Fatal("a kill the log missed passed the check")
	}
}
