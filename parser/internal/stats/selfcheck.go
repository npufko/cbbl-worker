package stats

import (
	"regexp"
	"sort"
	"strconv"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

// The self-check: every demo carries the game's own scoreboard counters on each player controller
// (m_pActionTrackingServices), so the log can be checked against an answer key with no external
// source — the only one a LAN has. At every round start, as each player leaves, and at the end,
// each counter is compared with the same stat recomputed from the log the way packages/core counts
// it. A difference names the round it first appeared in.

// A counted stat: the game's property and how the log counts it.
type checkStat struct {
	name   string
	prop   string
	scored bool // false: the definition is known to differ (reported, not a failure)
	// perRound: the game adds the round's total when the round is officially over (seen: damage,
	// utility damage, enemies flashed, MVPs); the kill counters move the instant a kill happens.
	perRound bool
}

var checkStats = []checkStat{
	{"kills", "m_pActionTrackingServices.m_iKills", true, false},
	{"deaths", "m_pActionTrackingServices.m_iDeaths", true, false},
	{"assists", "m_pActionTrackingServices.m_iAssists", true, false},
	{"hsKills", "m_pActionTrackingServices.m_iHeadShotKills", true, false},
	{"damage", "m_pActionTrackingServices.m_iDamage", true, true},
	{"utilDamage", "m_pActionTrackingServices.m_iUtilityDamage", true, true},
	{"3k", "m_pActionTrackingServices.m_iEnemy3Ks", true, false},
	{"4k", "m_pActionTrackingServices.m_iEnemy4Ks", true, false},
	{"5k", "m_pActionTrackingServices.m_iEnemy5Ks", true, false},
	{"knifeKills", "m_pActionTrackingServices.m_iEnemyKnifeKills", true, false},
	{"taserKills", "m_pActionTrackingServices.m_iEnemyTaserKills", true, false},
	{"mvps", "m_iMVPs", true, true},
	{"enemiesFlashed", "m_pActionTrackingServices.m_iEnemiesFlashed", false, true}, // ours: blinds ≥ 1.1 s
}

// SelfCheck is the comparison, shipped in the parse output.
type SelfCheck struct {
	Checks int         `json:"checks"` // round boundaries compared (plus the end)
	OK     bool        `json:"ok"`     // every scored stat equal for every player at the end (known differences aside)
	Stats  []StatCheck `json:"stats"`
	Diffs  []CheckDiff `json:"diffs"`  // players whose stat differs at the end (or when last seen)
	Broken []Broken    `json:"broken"` // invariants the map breaks (invariants.go); any fails the check
}

type StatCheck struct {
	Stat    string `json:"stat"`
	Scored  bool   `json:"scored"`
	Players int    `json:"players"` // players compared
	Same    int    `json:"same"`
	Ours    int    `json:"ours"` // summed over the players compared
	Game    int    `json:"game"`
}

type CheckDiff struct {
	Stat    string `json:"stat"`
	SteamID string `json:"steamId"`
	Name    string `json:"name"`
	Ours    int    `json:"ours"`
	Game    int    `json:"game"`
	Round   int    `json:"round"` // first round after which they differed
	// Known: the difference is one cbbl makes on purpose, and why (it does not fail the check).
	Known string `json:"known,omitempty"`
}

// Differences cbbl makes on purpose.
const knownSlayAssist = "assist for damaging a player who died to World after a round ended (a match-end slay; FACEIT does not count it either)"

// checkState: the latest reading per player and when each difference first appeared.
type checkState struct {
	checks int
	last   map[string]reading // steamId → their latest reading
	first  map[[2]string]int  // (stat, steamId) → round the current difference started
	// Rounds the game has folded into its per-round counters: set when a round is officially over
	// and at the next round start (once the game ends, every round is).
	committed int
	// Players who left this frame, read on the next one (see onDisconnect).
	leaving []*common.Player
	// Counts the game has and the log leaves out on purpose, per (stat, steamId), and why.
	known    map[[2]string]int
	knownWhy map[[2]string]string
}

// reading is one player's counters and the log's totals for them at the same instant. A player who
// leaves (FACEIT kicks everyone the moment the match ends) is last read as they go, so the end
// compares them as of then, not against rounds they never reached in the game's counters.
type reading struct {
	name       string
	game, ours map[string]int
}

func newCheckState() checkState {
	return checkState{last: map[string]reading{}, first: map[[2]string]int{}, known: map[[2]string]int{}, knownWhy: map[[2]string]string{}}
}

// rewindCheck forgets differences that started in rounds being dropped (a restart or restore).
func (s *checkState) rewindCheck(n int) {
	for k, r := range s.first {
		if r > n {
			delete(s.first, k)
		}
	}
	s.committed = min(s.committed, n)
}

// leaveOut records a count the game makes that the log leaves out on purpose.
func (s *checkState) leaveOut(stat string, pl *common.Player, why string) {
	if pl == nil || pl.SteamID64 == 0 {
		return
	}
	k := [2]string{stat, strconv.FormatUint(pl.SteamID64, 10)}
	s.known[k]++
	s.knownWhy[k] = why
}

var knifeName = regexp.MustCompile(`(?i)knife|bayonet|karambit|daggers`)
var zeusName = regexp.MustCompile(`(?i)zeus`)

// logTotals counts every checked stat from the rounds kept so far, as packages/core does.
func logTotals(rounds []Round) map[string]map[string]int {
	out := map[string]map[string]int{}
	add := func(id, stat string, n int) {
		if id == "" {
			return
		}
		if out[id] == nil {
			out[id] = map[string]int{}
		}
		out[id][stat] += n
	}
	for _, r := range rounds {
		side := map[string]string{}
		for _, p := range r.Players {
			side[p.SteamID] = p.Side
		}
		enemies := func(a, b string) bool {
			return a != "" && b != "" && side[a] != "" && side[b] != "" && side[a] != side[b]
		}
		if r.MVP != "" {
			add(r.MVP, "mvps", 1)
		}
		killsBy := map[string]int{}
		for _, k := range r.Kills {
			add(k.Victim, "deaths", 1)
			// An assist counts even when the victim killed themself (own molotov, a fall): the game
			// credits the enemy who damaged them.
			if enemies(k.Assister, k.Victim) {
				add(k.Assister, "assists", 1)
			}
			if !enemies(k.Killer, k.Victim) {
				continue
			}
			add(k.Killer, "kills", 1)
			killsBy[k.Killer]++
			if k.HS {
				add(k.Killer, "hsKills", 1)
			}
			if knifeName.MatchString(k.Weapon) {
				add(k.Killer, "knifeKills", 1)
			} else if zeusName.MatchString(k.Weapon) {
				add(k.Killer, "taserKills", 1)
			}
		}
		for id, n := range killsBy {
			switch {
			case n == 3:
				add(id, "3k", 1)
			case n == 4:
				add(id, "4k", 1)
			case n >= 5:
				add(id, "5k", 1)
			}
		}
		for _, d := range r.Damage {
			if !enemies(d.Attacker, d.Victim) {
				continue
			}
			add(d.Attacker, "damage", d.HP)
			if !d.Impact && (d.Weapon == "HE Grenade" || d.Weapon == "Incendiary Grenade" || d.Weapon == "Molotov") {
				add(d.Attacker, "utilDamage", d.HP)
			}
		}
		for _, b := range r.Blinds {
			if !b.Teammate && b.Seconds >= minFlashSeconds {
				add(b.By, "enemiesFlashed", 1)
			}
		}
	}
	return out
}

// selfCheck reads the game's counters for these players and compares each with the log at the same
// point: the kill counters with everything so far (the round in progress included), the per-round
// ones with the rounds the game has committed. Only called when the rounds kept match the game's
// own round count (see the call sites).
func (c *Collector) selfCheck(players []*common.Player) {
	s := &c.check
	rounds := c.rounds
	if c.cur != nil && !c.postRound {
		rounds = append(rounds[:len(rounds):len(rounds)], *c.cur)
	}
	committed := s.committed
	if c.p.GameState().GamePhase() == common.GamePhaseGameEnded {
		committed = len(c.rounds)
	}
	var live, done map[string]map[string]int
	read := false
	for _, pl := range players {
		if pl == nil || pl.SteamID64 == 0 || pl.Entity == nil {
			continue
		}
		id := strconv.FormatUint(pl.SteamID64, 10)
		game, mine := map[string]int{}, map[string]int{}
		for _, st := range checkStats {
			v, ok := pl.Entity.PropertyValue(st.prop)
			if !ok || v.Any == nil {
				continue
			}
			if live == nil {
				live, done = logTotals(rounds), logTotals(c.rounds[:committed])
			}
			g, o, n := v.Int(), live[id][st.name], len(rounds)
			if st.perRound {
				o, n = done[id][st.name], committed
			}
			game[st.name], mine[st.name] = g, o
			k := [2]string{st.name, id}
			if g == o || g == o+s.known[k] {
				delete(s.first, k)
			} else if _, seen := s.first[k]; !seen {
				s.first[k] = n
			}
		}
		if len(game) == 0 {
			continue
		}
		read = true
		s.last[id] = reading{name: pl.Name, game: game, ours: mine}
	}
	if read {
		s.checks++
	}
}

// commitRounds: the game has folded every round so far into its per-round counters.
func (c *Collector) commitRounds() { c.check.committed = len(c.rounds) }

func (c *Collector) onRoundOfficial(events.RoundEndOfficial) { c.commitRounds() }

// onDisconnect queues a leaving player to be read on the next frame: in the tick they go, the game
// kills their body, and that death is not on the counter yet when the disconnect arrives (four
// abandoners in a Premier match each read one death short). Later readings of a departed controller
// are not taken: after the match the game adds deaths and an MVP that were never played.
func (c *Collector) onDisconnect(e events.PlayerDisconnected) {
	if c.cur == nil || c.pendingRestart || c.segmentStart || !c.live() || e.Player == nil {
		return
	}
	if c.p.GameState().GamePhase() == common.GamePhaseGameEnded {
		c.creditMVP(c.playing()) // the last round's, if the game has named it before its MVP leaves
	}
	c.check.leaving = append(c.check.leaving, e.Player)
}

func (c *Collector) onCheckFrame(events.FrameDone) {
	if len(c.check.leaving) == 0 {
		return
	}
	leaving := c.check.leaving
	c.check.leaving = nil
	if c.cur == nil || c.pendingRestart || c.segmentStart || !c.live() {
		return
	}
	c.selfCheck(leaving)
}

// finalMVPs: a departed player's MVP counter as it stands at the end, for the final round's MVP the
// game names after the kick (creditMVP puts it in the log from the same counters).
func (s *checkState) finalMVPs(players []*common.Player, ours map[string]map[string]int) {
	for _, pl := range players {
		id := strconv.FormatUint(pl.SteamID64, 10)
		rd, ok := s.last[id]
		if !ok {
			continue
		}
		if _, had := rd.game["mvps"]; had {
			rd.game["mvps"], rd.ours["mvps"] = pl.MVPs(), ours[id]["mvps"]
		}
	}
}

// checkResult compares every player's latest reading.
func (c *Collector) checkResult() *SelfCheck {
	s := &c.check
	if s.checks == 0 {
		return nil
	}
	out := &SelfCheck{Checks: s.checks, OK: true, Stats: []StatCheck{}, Diffs: []CheckDiff{}}
	ids := make([]string, 0, len(s.last))
	for id := range s.last {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, st := range checkStats {
		sc := StatCheck{Stat: st.name, Scored: st.scored}
		for _, id := range ids {
			rd := s.last[id]
			g, ok := rd.game[st.name]
			if !ok {
				continue
			}
			o := rd.ours[st.name]
			sc.Players++
			sc.Ours += o
			sc.Game += g
			if o == g {
				sc.Same++
				continue
			}
			k := [2]string{st.name, id}
			d := CheckDiff{Stat: st.name, SteamID: id, Name: rd.name, Ours: o, Game: g, Round: s.first[k]}
			if n := s.known[k]; n > 0 && g == o+n {
				d.Known = s.knownWhy[k]
			} else if st.scored {
				out.OK = false
			}
			out.Diffs = append(out.Diffs, d)
		}
		if sc.Players > 0 {
			out.Stats = append(out.Stats, sc)
		}
	}
	return out
}
