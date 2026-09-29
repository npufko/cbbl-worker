package stats

import (
	"fmt"
	"sort"
)

// Invariants: rules every correct map obeys, whatever the demo. The self-check compares the log with
// the game's counters; these catch what both could share (a replayed round, a phantom player, a
// death that never happened). A broken rule fails the self-check.

// Broken is one rule a map breaks.
type Broken struct {
	Rule   string `json:"rule"`
	Round  int    `json:"round,omitempty"` // 0 = the whole map
	Detail string `json:"detail"`
}

func checkInvariants(rounds []Round, teams []Team) []Broken {
	out := []Broken{}
	for _, t := range teams {
		if t.Score != t.GameScore {
			out = append(out, Broken{Rule: "score equals the game's", Detail: fmt.Sprintf("%q: %d rounds won, game says %d", t.Name, t.Score, t.GameScore)})
		}
	}
	for i, r := range rounds {
		n := i + 1
		broke := func(rule, format string, a ...any) {
			out = append(out, Broken{Rule: rule, Round: n, Detail: fmt.Sprintf(format, a...)})
		}
		if r.Winner != "CT" && r.Winner != "T" {
			broke("one winner", "winner %q", r.Winner)
		}
		side := map[string]string{}
		perSide := map[string]int{}
		for _, p := range r.Players {
			side[p.SteamID] = p.Side
			perSide[p.Side]++
		}
		if perSide["CT"] > 5 || perSide["T"] > 5 {
			broke("at most 5 a side", "%d CT, %d T", perSide["CT"], perSide["T"])
		}
		taken := map[string]int{}
		for _, d := range r.Damage {
			taken[d.Victim] += d.HP
		}
		for _, id := range sortedKeys(taken) {
			if taken[id] > 100 {
				broke("at most 100 HP taken", "%s took %d", id, taken[id])
			}
		}
		deaths, world := map[string]int{}, map[string]bool{}
		for _, k := range r.Kills {
			deaths[k.Victim]++
			if k.Weapon == "World" {
				world[k.Victim] = true
			}
			if side[k.Victim] == "" {
				broke("the dead played the round", "%s died at %.2f", k.Victim, k.T)
			}
		}
		for _, id := range sortedKeys(deaths) {
			// A second life is possible only by taking over the bot that replaced you after you left.
			if c := deaths[id]; c > 2 || (c == 2 && !world[id]) {
				broke("one death a round", "%s died %d times", id, c)
			}
		}
		for _, p := range r.Players {
			if p.HP > 0 && deaths[p.SteamID] > 0 && !world[p.SteamID] {
				broke("the dead are not alive at the end", "%s died and ended on %d HP", p.SteamID, p.HP)
			}
		}
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
