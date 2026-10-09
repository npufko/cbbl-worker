// Package stats turns demoinfocs events into cbbl's per-map JSON (plan §6 MapResult + match_rounds).
package stats

import (
	"math"
	"sort"
	"strconv"

	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/golang/geo/r3"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

const (
	tradeWindowSeconds = 5.0
	minFlashSeconds    = 1.1 // ignore trivial flashes
	massWorldDeaths    = 5   // this many "World" deaths at one instant void the round (voidRound)
)

type Kill struct {
	T        float64 `json:"t"` // seconds since round start
	Killer   string  `json:"killer,omitempty"`
	Victim   string  `json:"victim"`
	Assister string  `json:"assister,omitempty"`
	Weapon   string  `json:"weapon"`
	HS       bool    `json:"hs"`
	Traded   bool    `json:"traded"`
	Opening  bool    `json:"opening"`
	// Event log v1 (see log.go).
	FlashAssist  bool    `json:"flashAssist,omitempty"` // the assister's flash, not damage
	Wallbang     bool    `json:"wallbang,omitempty"`
	ThroughSmoke bool    `json:"smoke,omitempty"`
	NoScope      bool    `json:"noscope,omitempty"`
	KillerBlind  bool    `json:"blind,omitempty"`
	Distance     float64 `json:"dist,omitempty"`
	KillerPos    *[3]int `json:"kpos,omitempty"`
	VictimPos    *[3]int `json:"vpos,omitempty"`
	KillerHP     int     `json:"khp,omitempty"`
	KillerSide   string  `json:"kside,omitempty"`
	VictimSide   string  `json:"vside,omitempty"`
	Around       []At    `json:"around,omitempty"` // everyone else alive at the kill
}

type Clutch struct {
	SteamID string `json:"steamId"`
	Vs      int    `json:"vs"`
	Won     bool   `json:"won"`
}

type Round struct {
	N      int     `json:"n"`
	Winner string  `json:"winner"` // "CT" | "T"; mapped to series sides by the worker using team rosters
	CTTeam int     `json:"ctTeam"` // index into Result.Teams of the team on CT this round
	Reason int     `json:"reason"` // events.RoundEndReason
	Equip  [2]int  `json:"equip"`  // [CT, T] freeze-time-end equipment value
	Kills  []Kill  `json:"kills"`
	Clutch *Clutch `json:"clutch,omitempty"`
	// Event log v1 (see log.go).
	Start     float64       `json:"start"`     // demo seconds when the round started (times below are relative to it)
	FreezeEnd float64       `json:"freezeEnd"` // seconds since start
	End       float64       `json:"end"`       // seconds since start
	MVP       string        `json:"mvp,omitempty"`
	Bomb      *Bomb         `json:"bomb,omitempty"`
	Players   []RoundPlayer `json:"players"`
	Damage    []Damage      `json:"damage"`
	Nades     []Nade        `json:"nades"`
	Blinds    []Blind       `json:"blinds"`
	Shots     []Shots       `json:"shots"`
	Sightings []Sighting    `json:"sightings"`
}

func newRound(n int, start float64) *Round {
	return &Round{N: n, Start: r2(start), Kills: []Kill{}, Players: []RoundPlayer{}, Damage: []Damage{}, Nades: []Nade{}, Blinds: []Blind{}, Shots: []Shots{}, Sightings: []Sighting{}}
}

type Player struct {
	SteamID        string  `json:"steamId"`
	Name           string  `json:"name"`
	K              int     `json:"k"`
	D              int     `json:"d"`
	A              int     `json:"a"`
	HS             int     `json:"hs"`
	Damage         int     `json:"damage"`
	ADR            float64 `json:"adr"`
	KAST           float64 `json:"kast"`
	Rating         float64 `json:"rating"`
	OpeningK       int     `json:"openingK"`
	OpeningD       int     `json:"openingD"`
	ClutchesWon    int     `json:"clutchesWon"`
	ClutchesAtt    int     `json:"clutchesAtt"`
	FlashAssists   int     `json:"flashAssists"`
	EnemiesFlashed int     `json:"enemiesFlashed"`
	UtilDmg        int     `json:"utilDmg"`
	// Rank, straight off the player entity. CS2 RankType: 7 Wingman, 11 Premier, 12 Competitive.
	// Rank is a CS Rating (e.g. 23450) under Premier and 1-18 under Competitive — the same field
	// means two different things, so RankType has to travel with it.
	RankType   int     `json:"rankType"`
	Rank       int     `json:"rank"`
	Wins       int     `json:"wins"`
	RankNew    int     `json:"rankNew"` // after the match, from the end-of-match rank update
	RankChange float32 `json:"rankChange"`
	kastRounds int
}

// Mode names what the ranks mean, from the rank type the players were queued under.
const (
	ModePremier     = "premier"
	ModeCompetitive = "competitive"
	ModeWingman     = "wingman"
)

type Result struct {
	LogVersion int       `json:"logVersion"` // event log version (log.go); 0 = before the log existed
	Mode       string    `json:"mode"`       // premier | competitive | wingman | "" when not a Valve server
	Map        string    `json:"map"`
	TickRate   float64   `json:"tickrate"`
	Rounds     []Round   `json:"rounds"`
	Players    []*Player `json:"players"`
	// How many times the game restarted (knife round, LO3). Everything before the last one is
	// discarded; reported so a round-count dispute can be diagnosed from the worker log alone.
	Restarts int `json:"restarts"`
	// How many times a round backup was restored mid-match (a LAN "match medic" after a lag-out).
	// Play rewinds to an earlier round, so the rounds after it are dropped and replayed.
	Restores int `json:"restores"`
	// The two teams as the demo itself saw them. FACEIT and Valve matches get rosters from their own
	// APIs; a LAN demo has nothing else, so this is where its teams and map winner come from.
	Teams []Team `json:"teams"`
	// The log checked against the game's own scoreboard counters (selfcheck.go). nil when the demo
	// has none to read.
	SelfCheck *SelfCheck `json:"selfCheck,omitempty"`
}

// Team is one side of the map, followed across the half-time swap by who is on it.
type Team struct {
	Name    string   `json:"name"`    // in-game team name (mp_teamname_*), "" if the server set none
	Players []string `json:"players"` // SteamID64s of everyone who played a live round for it
	Score   int      `json:"score"`   // rounds won, counted from Rounds
	// The game's own final score, read off the team entity at the end. Should equal Score; a
	// difference means rounds were lost or double-counted and the map needs a look.
	GameScore int `json:"gameScore"`
}

type Collector struct {
	p        dem.Parser
	restarts int
	restores int
	rounds   []Round
	players  map[uint64]*Player
	// Player totals as they stood after each completed round (snapshots[i] = after round i+1), so a
	// round-backup restore can rewind them. A map is ~30 rounds of ~10 players: small.
	snapshots [][]Player
	// A MatchStart was seen and not yet judged. It is judged at the next live RoundStart, once the
	// game's own round count has settled; see settleRestart.
	pendingRestart bool
	// The first round of a continued recording segment is still to end: see settleSegment.
	segmentStart bool
	// Between a round's end and the next round's start: kills and damage in that window (exit frags,
	// a molotov still burning) belong to the round that just ended, as FACEIT and HLTV count them.
	// cur then points at that stored round.
	postRound bool
	// Deaths to "World" at one instant (see voidRound): when, how many, and the instant last voided.
	worldAt, voidAt float64
	worldN          int
	worldHeld       []heldKill // post-round "World" deaths of the current instant (flushWorld)
	teams           []*teamAcc
	cur             *Round
	roundStart      float64
	// per-round state
	contributed map[uint64]bool // KAST: kill, assist or traded this round
	died        map[uint64]float64
	killerOf    map[uint64]uint64
	clutch      map[common.Team]*Clutch
	// event log working state (log.go)
	log     logState
	lastPos map[uint64]posSample
	speed   map[uint64]float64
	// Each player's scoreboard MVP count as last seen (see creditMVP).
	mvps map[uint64]int
	// Each victim's health after the last hit on them (see hpTaken).
	hurtHP map[uint64]tickHP
	// Each player's health at the last frame, and the health this frame's hurt events account for
	// (see inferHits).
	hpSeen    map[uint64]int
	hurtFrame map[uint64]hurtSum
	heFrame   []heBlast
	killFrame map[uint64]frameKill
	check     checkState
}

func NewCollector(p dem.Parser) *Collector {
	c := &Collector{players: map[uint64]*Player{}, worldAt: -1, voidAt: -1, log: newLogState(), lastPos: map[uint64]posSample{}, speed: map[uint64]float64{}, check: newCheckState(), hurtHP: map[uint64]tickHP{}, hpSeen: map[uint64]int{}, hurtFrame: map[uint64]hurtSum{}, killFrame: map[uint64]frameKill{}}
	c.attach(p)
	return c
}

// Continue carries on collecting the same map from its next recording segment. A LAN server starts
// a new demo file mid-map (FRAG: <map>_<series>.dem, then <map>_<series>_1.dem), typically after a
// round-backup restore, and the game's score carries over. The new file is judged like a MatchStart
// at its first live round: the game's round count equal to ours is a plain continuation; lower means
// the restore replayed rounds (rewind to it, which also drops the phantom round the restore itself
// ends); 0 is a genuine restart.
func (c *Collector) Continue(p dem.Parser) {
	c.flushWorld()
	c.endPostRound()
	c.attach(p)
	c.cur = nil
	c.pendingRestart = true
	c.segmentStart = true
}

// settleSegment judges the first round of a continued segment when it ends, against the round count
// the game reports then (FRAG Midwest): N+1 is the next round; N or lower means a restore sent play
// back and this round replays round N (12-3 became 11-4), so it replaces it and what came after.
func (c *Collector) settleSegment() {
	c.segmentStart = false
	after := c.p.GameState().TotalRoundsPlayed()
	if after < 1 || after > len(c.rounds) {
		return
	}
	// Keep only what this round added (voidRound already dropped anything before a restore), on top
	// of the totals as they stood before the round it replays.
	delta := c.roundDelta()
	cur := c.cur
	c.rewind(after - 1)
	c.addDelta(delta)
	cur.N = after
	c.cur = cur
}

// voidRound: most of the server died to "World" at one instant. That is a new recording starting
// (t=0) or a round backup being loaded, never play, so the round in progress starts over from here:
// its kills, deaths and damage so far are undone.
func (c *Collector) voidRound(t float64) {
	c.voidAt = t
	c.players = map[uint64]*Player{}
	if n := len(c.snapshots); n > 0 {
		for i := range c.snapshots[n-1] {
			x := c.snapshots[n-1][i]
			if id, err := strconv.ParseUint(x.SteamID, 10, 64); err == nil {
				c.players[id] = &x
			}
		}
	}
	c.cur.Kills = []Kill{}
	c.cur.Clutch = nil
	c.cur.Damage, c.cur.Nades, c.cur.Blinds, c.cur.Shots, c.cur.Sightings, c.cur.Bomb = []Damage{}, []Nade{}, []Blind{}, []Shots{}, []Sighting{}, nil
	for i := range c.cur.Players {
		c.cur.Players[i].HP, c.cur.Players[i].UtilLeft = 0, 0
	}
	c.log = newLogState()
	c.roundStart = t
	c.contributed = map[uint64]bool{}
	c.died = map[uint64]float64{}
	c.killerOf = map[uint64]uint64{}
	c.clutch = map[common.Team]*Clutch{}
}

// roundDelta is what the round in progress has added to each player's totals so far.
func (c *Collector) roundDelta() map[uint64]Player {
	before := map[string]Player{}
	if n := len(c.snapshots); n > 0 {
		for _, x := range c.snapshots[n-1] {
			before[x.SteamID] = x
		}
	}
	out := map[uint64]Player{}
	for id, x := range c.players {
		b := before[x.SteamID]
		out[id] = Player{
			SteamID: x.SteamID, Name: x.Name, RankType: x.RankType, Rank: x.Rank, Wins: x.Wins,
			K: x.K - b.K, D: x.D - b.D, A: x.A - b.A, HS: x.HS - b.HS, Damage: x.Damage - b.Damage,
			OpeningK: x.OpeningK - b.OpeningK, OpeningD: x.OpeningD - b.OpeningD,
			ClutchesWon: x.ClutchesWon - b.ClutchesWon, ClutchesAtt: x.ClutchesAtt - b.ClutchesAtt,
			FlashAssists: x.FlashAssists - b.FlashAssists, EnemiesFlashed: x.EnemiesFlashed - b.EnemiesFlashed,
			UtilDmg: x.UtilDmg - b.UtilDmg, kastRounds: x.kastRounds - b.kastRounds,
		}
	}
	return out
}

func (c *Collector) addDelta(delta map[uint64]Player) {
	for id, d := range delta {
		x, ok := c.players[id]
		if !ok {
			x = &Player{SteamID: d.SteamID}
			c.players[id] = x
		}
		x.Name, x.RankType, x.Rank, x.Wins = d.Name, d.RankType, d.Rank, d.Wins
		x.K += d.K
		x.D += d.D
		x.A += d.A
		x.HS += d.HS
		x.Damage += d.Damage
		x.OpeningK += d.OpeningK
		x.OpeningD += d.OpeningD
		x.ClutchesWon += d.ClutchesWon
		x.ClutchesAtt += d.ClutchesAtt
		x.FlashAssists += d.FlashAssists
		x.EnemiesFlashed += d.EnemiesFlashed
		x.UtilDmg += d.UtilDmg
		x.kastRounds += d.kastRounds
	}
}

func (c *Collector) attach(p dem.Parser) {
	c.p = p
	p.RegisterEventHandler(c.onMatchStart)
	p.RegisterEventHandler(c.onRoundStart)
	p.RegisterEventHandler(c.onFreezeEnd)
	p.RegisterEventHandler(c.onKill)
	p.RegisterEventHandler(c.onHurt)
	p.RegisterEventHandler(c.onFlashed)
	p.RegisterEventHandler(c.onRoundEnd)
	p.RegisterEventHandler(c.onRankUpdate)
	p.RegisterEventHandler(c.onFrame)
	p.RegisterEventHandler(c.onHealthFrame)
	p.RegisterEventHandler(c.onFire)
	p.RegisterEventHandler(c.onThrow)
	p.RegisterEventHandler(c.onNadeEvent)
	p.RegisterEventHandler(c.onNadeDestroy)
	p.RegisterEventHandler(c.onPlanted)
	p.RegisterEventHandler(c.onDefused)
	p.RegisterEventHandler(c.onExploded)
	p.RegisterEventHandler(c.onMVP)
	p.RegisterEventHandler(c.onDisconnect)
	p.RegisterEventHandler(c.onRoundOfficial)
	p.RegisterEventHandler(c.onCheckFrame)
	c.lastPos = map[uint64]posSample{} // positions do not carry across recording segments
}

// MatchStart fires for two different things, and they need opposite handling:
//
//   - a real restart: FACEIT and ESEA play a knife round for side choice and then restart, and
//     servers LO3 before going live. Everything collected before it has to go — a counted knife
//     round makes the round total disagree with the official score and inflates every player's
//     K/D, ADR and Rating 2.0.
//   - a round-backup restore: LAN admins ("match medics") reload a backup after a lag-out, and play
//     resumes at 7-5 or wherever it was. Resetting there would silently drop every round before
//     it and still parse "successfully" with half the match.
//
// The game's own score tells them apart, but it has not necessarily settled at the MatchStart tick,
// so the round in progress is dropped now (it will be replayed either way) and the decision waits
// for the next live RoundStart.
func (c *Collector) onMatchStart(events.MatchStart) {
	c.flushWorld()
	c.endPostRound()
	c.cur = nil
	c.pendingRestart = true
}

// settleRestart judges a pending MatchStart against the number of rounds the game says were played.
func (c *Collector) settleRestart(played int) {
	if !c.pendingRestart {
		return
	}
	c.pendingRestart = false
	switch {
	case played <= 0:
		c.reset() // knife round / LO3: the game is back at 0-0
	case played < len(c.rounds):
		c.rewind(played) // match medic: resume at round played+1
	default:
		// Nothing we collected is being replayed (e.g. a MatchStart with no rewind): keep it all.
	}
}

// rewind drops every round after the first n and restores player totals to how they stood then.
func (c *Collector) rewind(n int) {
	c.postRound = false
	c.restores++
	c.rounds = c.rounds[:n]
	c.snapshots = c.snapshots[:n]
	c.check.rewindCheck(n)
	c.players = map[uint64]*Player{}
	c.cur = nil
	if n == 0 {
		return
	}
	for i := range c.snapshots[n-1] {
		x := c.snapshots[n-1][i] // a copy: later rounds must not write into the snapshot
		id, err := strconv.ParseUint(x.SteamID, 10, 64)
		if err != nil {
			continue
		}
		c.players[id] = &x
	}
}

// snapshot records the player totals after the round just completed.
func (c *Collector) snapshot() {
	s := make([]Player, 0, len(c.players))
	for _, x := range c.players {
		s = append(s, *x)
	}
	c.snapshots = append(c.snapshots, s)
}

// reset drops all accumulated match state, keeping only the parser handle.
func (c *Collector) reset() {
	c.postRound = false
	c.restarts++
	c.rounds = nil
	c.check = newCheckState()
	c.teams = nil
	c.snapshots = nil
	c.players = map[uint64]*Player{}
	c.cur = nil
	c.roundStart = 0
	c.contributed = map[uint64]bool{}
	c.died = map[uint64]float64{}
	c.killerOf = map[uint64]uint64{}
	c.clutch = map[common.Team]*Clutch{}
}

func (c *Collector) live() bool {
	gs := c.p.GameState()
	return gs.IsMatchStarted() && !gs.IsWarmupPeriod()
}

func (c *Collector) player(pl *common.Player) *Player {
	if pl == nil || pl.SteamID64 == 0 { // bots / world
		return nil
	}
	if x, ok := c.players[pl.SteamID64]; ok {
		x.Name = pl.Name
		return x
	}
	x := &Player{SteamID: strconv.FormatUint(pl.SteamID64, 10), Name: pl.Name}
	c.players[pl.SteamID64] = x
	return x
}

func (c *Collector) now() float64 { return c.p.CurrentTime().Seconds() }

func (c *Collector) onRoundStart(events.RoundStart) {
	c.flushWorld()
	c.creditMVP(c.playing())
	c.endPostRound()
	if !c.live() {
		return
	}
	played := c.p.GameState().TotalRoundsPlayed()
	c.settleRestart(played)
	c.followRestore(played)
	if len(c.rounds) > 0 && !c.segmentStart {
		c.commitRounds()
		c.selfCheck(c.playing())
	}
	c.startRound()
}

// followRestore: the game's own count of rounds played fell below ours with no MatchStart, so a round
// backup was loaded and play resumes at round played+1 (FRAG Midwest 3627, 3825, 3994, 4057: a match
// medic mid-round, no restart event, no mass slay; the rounds after it were counted twice). The
// rounds after it are dropped and the totals rewound, as for a restore judged at a MatchStart.
func (c *Collector) followRestore(played int) bool {
	if c.pendingRestart || c.segmentStart || played < 0 || played >= len(c.rounds) {
		return false
	}
	c.rewind(played)
	return true
}

// startRound opens a new round in progress, numbered after the rounds kept.
func (c *Collector) startRound() {
	c.cur = newRound(len(c.rounds)+1, c.now())
	c.roundStart = c.now()
	c.log = newLogState()
	c.contributed = map[uint64]bool{}
	c.died = map[uint64]float64{}
	c.killerOf = map[uint64]uint64{}
	c.clutch = map[common.Team]*Clutch{}
}

func (c *Collector) onFreezeEnd(events.RoundFreezetimeEnd) {
	if c.cur == nil {
		return
	}
	c.cur.FreezeEnd = c.roundT()
	c.logFreezeEnd()
	// A recording that starts with the match opens before the players are in (FRAG Midwest 3892, 3962:
	// round 1's MVP read as a first sighting, not a rise). Everyone is in by now and the round has no
	// MVP yet: a missing baseline is taken here (creditMVP).
	if c.mvps == nil {
		c.mvps = map[uint64]int{}
	}
	for _, pl := range c.playing() {
		if _, seen := c.mvps[pl.SteamID64]; !seen {
			c.mvps[pl.SteamID64] = pl.MVPs()
		}
	}
	for _, pl := range c.playing() {
		switch pl.Team {
		case common.TeamCounterTerrorists:
			c.cur.Equip[0] += pl.EquipmentValueFreezeTimeEnd()
		case common.TeamTerrorists:
			c.cur.Equip[1] += pl.EquipmentValueFreezeTimeEnd()
		}
	}
}

func (c *Collector) onKill(e events.Kill) {
	if c.cur == nil || e.Victim == nil {
		return
	}
	t := c.now()
	// "World": no enemy killer (none, or the victim themself) and the world as the weapon.
	if (e.Killer == nil || e.Killer == e.Victim) && e.Weapon != nil && (e.Weapon.Type == common.EqWorld || e.Weapon.String() == "World") {
		if t == c.voidAt {
			return // the rest of a voided instant
		}
		if t != c.worldAt {
			c.worldAt, c.worldN = t, 0
		}
		if c.postRound {
			if c.p.GameState().GamePhase() == common.GamePhaseGameEnded {
				c.leaveOutSlain(e) // FACEIT slaying everyone once the match is over: not part of it
				return
			}
			// After the round, "World" is either one player dying (a fall: the game counts it, toasty,
			// FRAG Jersey 2588 r7) or a restore / recording artifact slaying most of the server at once.
			// Which one is known only when the instant is over: held until then (flushWorld).
			if len(c.worldHeld) > 0 && c.worldHeld[0].t != t {
				c.flushWorld()
			}
			c.worldHeld = append(c.worldHeld, heldKill{e: e, t: t})
			return
		}
		if c.worldN++; c.worldN >= massWorldDeaths {
			c.voidRound(t)
			return
		}
		// A bot dying to "World" is the game removing it: its player came back and takes over its
		// body (kevin, 1-5b0b4db1 r5). Nobody died; logging it gave a phantom player "0" a death.
		if e.Victim.SteamID64 == 0 {
			c.checkClutch()
			return
		}
	}
	c.recordKill(e, t)
}

// heldKill is a post-round "World" death waiting for its instant to end (see flushWorld).
type heldKill struct {
	e events.Kill
	t float64
}

// flushWorld settles the post-round "World" deaths of the instant just over: a few are real deaths,
// most of the server at once is not play (the game still credits an assist for the slain).
func (c *Collector) flushWorld() {
	held := c.worldHeld
	c.worldHeld = nil
	for _, h := range held {
		if len(held) >= massWorldDeaths || c.cur == nil {
			c.leaveOutSlain(h.e)
			continue
		}
		c.recordKill(h.e, h.t)
	}
}

// leaveOutSlain: a "World" death cbbl does not count. The game credits an assist to whoever damaged
// the slain player; the self-check is told so it does not report that as a difference.
func (c *Collector) leaveOutSlain(e events.Kill) {
	if e.Assister != nil && e.Assister.Team != e.Victim.Team {
		c.check.leaveOut("assists", e.Assister, knownSlayAssist)
	}
}

// recordKill logs a kill (or a death with no enemy killer) and credits it.
func (c *Collector) recordKill(e events.Kill, t float64) {
	c.killFrame[e.Victim.SteamID64] = frameKill{by: e.Killer, weapon: e.Weapon}
	k := Kill{T: t - c.roundStart, Victim: strconv.FormatUint(e.Victim.SteamID64, 10), HS: e.IsHeadshot, Opening: len(c.cur.Kills) == 0}
	if e.Weapon != nil {
		k.Weapon = e.Weapon.String()
	}
	k.T = r2(k.T)
	k.FlashAssist, k.Wallbang, k.ThroughSmoke, k.NoScope, k.KillerBlind = e.AssistedFlash, e.IsWallBang(), e.ThroughSmoke, e.NoScope, e.AttackerBlind
	k.Distance = r1(float64(e.Distance))
	vp := vec3(e.Victim.Position())
	k.VictimPos, k.VictimSide = &vp, sideOf(e.Victim.Team)
	if e.Killer != nil {
		kp := vec3(e.Killer.Position())
		k.KillerPos, k.KillerHP, k.KillerSide = &kp, e.Killer.Health(), sideOf(e.Killer.Team)
	}
	k.Around = c.around(e.Killer, e.Victim)
	if rp := c.roundPlayer(e.Victim); rp != nil {
		rp.HP, rp.UtilLeft = 0, utilHeld(e.Victim)
	}
	if v := c.player(e.Victim); v != nil {
		v.D++
		if k.Opening {
			v.OpeningD++
		}
	}
	c.died[e.Victim.SteamID64] = t
	if e.Killer != nil && e.Killer.Team != e.Victim.Team {
		k.Killer = strconv.FormatUint(e.Killer.SteamID64, 10)
		if kp := c.player(e.Killer); kp != nil {
			kp.K++
			if e.IsHeadshot {
				kp.HS++
			}
			if k.Opening {
				kp.OpeningK++
			}
			c.contributed[e.Killer.SteamID64] = true
		}
		c.killerOf[e.Victim.SteamID64] = e.Killer.SteamID64
		// Trade: the victim's killer is killed within the window → the earlier victim was traded.
		for victim, killer := range c.killerOf {
			if killer == e.Victim.SteamID64 && t-c.died[victim] <= tradeWindowSeconds {
				c.contributed[victim] = true
				for i := range c.cur.Kills {
					if c.cur.Kills[i].Victim == strconv.FormatUint(victim, 10) {
						c.cur.Kills[i].Traded = true
					}
				}
			}
		}
	}
	if e.Assister != nil && e.Assister.Team != e.Victim.Team {
		k.Assister = strconv.FormatUint(e.Assister.SteamID64, 10)
		if ap := c.player(e.Assister); ap != nil {
			ap.A++
			if e.AssistedFlash {
				ap.FlashAssists++
			}
			c.contributed[e.Assister.SteamID64] = true
		}
	}
	c.cur.Kills = append(c.cur.Kills, k)
	c.checkClutch()
}

// checkClutch records the first moment a team is down to one player against ≥1 enemies.
func (c *Collector) checkClutch() {
	alive := map[common.Team][]*common.Player{}
	for _, pl := range c.playing() {
		if pl.IsAlive() {
			alive[pl.Team] = append(alive[pl.Team], pl)
		}
	}
	for _, team := range []common.Team{common.TeamCounterTerrorists, common.TeamTerrorists} {
		other := common.TeamTerrorists
		if team == common.TeamTerrorists {
			other = common.TeamCounterTerrorists
		}
		if len(alive[team]) == 1 && len(alive[other]) >= 1 && c.clutch[team] == nil {
			c.clutch[team] = &Clutch{SteamID: strconv.FormatUint(alive[team][0].SteamID64, 10), Vs: len(alive[other])}
		}
	}
}

// hpTaken is the health a hit actually took. The game's dmg_health drops the fraction of CS2's
// float damage while the victim's health falls by the rounded amount, so dmg_health loses up to 1 HP
// a hit (a kill's hits summed to 97-99, and ADR ran ~1.5% under FACEIT's). The victim's health
// before the hit minus after is exact; it is trusted only when it differs by that rounding, so a
// stale entity value can never inflate a hit.
//
// "Before" is the entity's health, except after an earlier hit on the same victim in the same tick:
// the entity still shows the health from before both, so the earlier hit's health-after is used. On
// a killing hit the library caps dmg_health at the entity's health, which then counts the earlier
// hit twice (a 15 and a killing 70 on a 70-HP player: the game credits the kill 55).
func (c *Collector) hpTaken(e events.PlayerHurt) int {
	if e.Player == nil {
		return e.HealthDamageTaken
	}
	tick := c.p.GameState().IngameTick()
	before := e.Player.Health()
	if h, ok := c.hurtHP[e.Player.SteamID64]; ok && h.tick == tick {
		before = h.health
	}
	c.hurtHP[e.Player.SteamID64] = tickHP{tick: tick, health: e.Health}
	if e.Health <= 0 {
		return min(e.HealthDamageTaken, max(before, 0))
	}
	if d := before - e.Health; d == e.HealthDamageTaken+1 || d == e.HealthDamageTaken {
		return d
	}
	return e.HealthDamageTaken
}

// tickHP is a victim's health after the last hit on them, and the tick it landed.
type tickHP struct{ tick, health int }

// hurtSum is what one frame's hurt events took from a player, and how many hits.
type hurtSum struct{ hp, hits int }

// heBlast is an HE grenade exploding this frame: who threw it and where.
type heBlast struct {
	by  *common.Player
	pos r3.Vector
}

// frameKill is a kill this frame: who, with what (see inferHit).
type frameKill struct {
	by     *common.Player
	weapon *common.Equipment
}

const (
	// fireReach: a victim this close (2D) to a burning flame may be taking that fire's damage. 95% of
	// logged fire hits land within 77 units of the thrower's nearest flame (1,624 hits, 13 demos).
	fireReach = 100.0
	// heReach: an HE exploding this close (3D) may have dealt a missing hit (its damage radius ~350).
	heReach = 400.0
)

// onHealthFrame: a demo can lack hurt events the game counted. FRAG Jersey 2579 r19: Galaxy's
// molotov took 2+3+3 HP from skylar with no player_hurt in the file at all, yet the game credited
// Galaxy the 8 (damage and utility damage). So each frame, a player's health drop beyond what that
// frame's hurt events explain (each hit may be 1 HP over its dmg_health, see hpTaken) is a missing hit,
// logged as inferred when its source is certain (inferHit). Anything else is left out, and the
// self-check against the game's counters reports it. 26 FACEIT, Valve and pro demos: no such drop.
func (c *Collector) onHealthFrame(events.FrameDone) {
	c.flushWorld()
	// A backup loaded mid-round fires no round event at all: the game's round count is the only sign.
	// Not after a round's end, where ours already counts the round the game is still closing.
	if c.cur != nil && !c.postRound && c.live() && c.followRestore(c.p.GameState().TotalRoundsPlayed()) {
		c.startRound()
	}
	hurt, blasts, kills := c.hurtFrame, c.heFrame, c.killFrame
	c.hurtFrame, c.heFrame, c.killFrame = map[uint64]hurtSum{}, nil, map[uint64]frameKill{}
	infer := c.cur != nil && c.live() && c.now() != c.voidAt
	for _, pl := range c.playing() {
		if pl.SteamID64 == 0 {
			continue
		}
		h := pl.Health()
		prev, seen := c.hpSeen[pl.SteamID64]
		c.hpSeen[pl.SteamID64] = h
		if !infer || !seen || h >= prev {
			continue
		}
		got := hurt[pl.SteamID64]
		if gap := prev - h - got.hp; gap > got.hits {
			k, killed := kills[pl.SteamID64]
			c.inferHit(pl, gap, blasts, k, killed)
		}
	}
}

// inferHit logs a missing hit of hp on victim when its source is certain:
//   - a killing hit (the victim died this frame): the kill names the killer and weapon (FRAG Midwest
//     3960, 4386: the final hit's player_hurt missing, 3 and 73 HP);
//   - otherwise exactly one thrower whose fire burns within fireReach, or whose HE exploded within
//     heReach this frame (FRAG Midwest 3994: an HE's 1 HP).
func (c *Collector) inferHit(victim *common.Player, hp int, blasts []heBlast, k frameKill, killed bool) {
	var by *common.Player
	var w string
	switch {
	case killed:
		if k.by == nil || k.by.SteamID64 == victim.SteamID64 || k.weapon == nil {
			return // a death to the world or to oneself: nobody to credit
		}
		by, w = k.by, k.weapon.String()
	case victim.IsAlive():
		pos := victim.Position()
		one := func(th *common.Player, weapon string) bool {
			if th == nil {
				return true
			}
			if by != nil && by.SteamID64 != th.SteamID64 {
				return false // two throwers: whose it was cannot be told
			}
			by, w = th, weapon
			return true
		}
		for _, inf := range c.p.GameState().Infernos() {
			for _, f := range inf.Fires().Active().List() {
				if math.Hypot(f.X-pos.X, f.Y-pos.Y) <= fireReach {
					fire := "Incendiary Grenade"
					if th := inf.Thrower(); th != nil && th.Team == common.TeamTerrorists {
						fire = "Molotov"
					}
					if !one(inf.Thrower(), fire) {
						return
					}
					break
				}
			}
		}
		for _, b := range blasts {
			if b.pos.Sub(pos).Norm() <= heReach && !one(b.by, "HE Grenade") {
				return
			}
		}
	}
	if by == nil {
		return
	}
	c.cur.Damage = append(c.cur.Damage, Damage{T: c.roundT(), Attacker: sid(by), Victim: sid(victim), HP: hp, Weapon: w, Inferred: true})
	if by.Team == victim.Team {
		return // team damage: logged, never credited (as onHurt)
	}
	if a := c.player(by); a != nil {
		a.Damage += hp
		if w == "HE Grenade" || w == "Molotov" || w == "Incendiary Grenade" {
			a.UtilDmg += hp
		}
	}
}

// creditMVP gives the round just completed its MVP from the scoreboard counters, for demos without
// the round_mvp announcement (FACEIT CS2 demos have none: every MVP read 0). Called before the next
// round starts and at the end: the one player whose count went up is the MVP. A counter that fell
// (restart, restore) only resets the baseline; two risers at once credit nobody rather than guess.
func (c *Collector) creditMVP(players []*common.Player) {
	if c.mvps == nil {
		c.mvps = map[uint64]int{}
	}
	var mvp uint64
	risen := 0
	for _, pl := range players {
		n := pl.MVPs()
		if prev, seen := c.mvps[pl.SteamID64]; seen && n > prev {
			mvp = pl.SteamID64
			risen++
		}
		c.mvps[pl.SteamID64] = n
	}
	if risen == 1 && len(c.rounds) > 0 && c.rounds[len(c.rounds)-1].MVP == "" {
		c.rounds[len(c.rounds)-1].MVP = strconv.FormatUint(mvp, 10)
	}
}

func (c *Collector) onHurt(e events.PlayerHurt) {
	if isCoach(e.Player) {
		return // the game's own 0-damage slay of the coach at freeze-time end
	}
	if c.now() == c.voidAt {
		return // the rest of a voided instant (a reset slays everyone: those hits are not play)
	}
	hp := c.hpTaken(e)
	if e.Player != nil {
		h := c.hurtFrame[e.Player.SteamID64]
		c.hurtFrame[e.Player.SteamID64] = hurtSum{hp: h.hp + hp, hits: h.hits + 1}
	}
	c.logHurt(e, hp)
	if c.cur == nil || e.Attacker == nil || e.Player == nil || e.Attacker.Team == e.Player.Team {
		return
	}
	a := c.player(e.Attacker)
	if a == nil {
		return
	}
	a.Damage += hp
	if e.Weapon != nil && e.Weapon.Class() == common.EqClassGrenade {
		a.UtilDmg += hp
	}
}

func (c *Collector) onFlashed(e events.PlayerFlashed) {
	if isCoach(e.Player) {
		return
	}
	c.logBlind(e)
	if c.cur == nil || e.Attacker == nil || e.Player == nil || e.Attacker.Team == e.Player.Team {
		return
	}
	if e.FlashDuration().Seconds() >= minFlashSeconds {
		if a := c.player(e.Attacker); a != nil {
			a.EnemiesFlashed++
		}
	}
}

func (c *Collector) onRoundEnd(e events.RoundEnd) {
	if c.cur == nil || c.postRound {
		return
	}
	if c.segmentStart {
		c.settleSegment()
	}
	c.cur.Reason = int(e.Reason)
	c.cur.Winner = map[common.Team]string{common.TeamCounterTerrorists: "CT", common.TeamTerrorists: "T"}[e.Winner]
	// KAST survival: anyone playing who did not die this round.
	for _, pl := range c.playing() {
		if _, dead := c.died[pl.SteamID64]; !dead {
			c.contributed[pl.SteamID64] = true
		}
	}
	for id := range c.contributed {
		if x := c.players[id]; x != nil {
			x.kastRounds++
		}
	}
	// Ensure every participant exists even with zero events, and refresh rank while the entity is
	// still around — a player who disconnects before the end takes their entity with them.
	for _, pl := range c.playing() {
		if x := c.player(pl); x != nil {
			if t := pl.RankType(); t > 0 {
				x.RankType = t
			}
			if r := pl.Rank(); r > 0 {
				x.Rank = r
			}
			if w := pl.CompetitiveWins(); w > 0 {
				x.Wins = w
			}
		}
	}
	// Both sides can be in a clutch (e.g. a 1v1): every attempt counts; the round shows the winner's.
	for team, cl := range c.clutch {
		cl.Won = team == e.Winner
		if x := c.playerByID(cl.SteamID); x != nil {
			x.ClutchesAtt++
			if cl.Won {
				x.ClutchesWon++
			}
		}
		if cl.Won || c.cur.Clutch == nil {
			c.cur.Clutch = cl
		}
	}
	c.cur.CTTeam = c.sideTeams()
	c.cur.End = c.roundT()
	c.logRoundEnd()
	c.rounds = append(c.rounds, *c.cur)
	c.snapshot()
	c.cur = &c.rounds[len(c.rounds)-1]
	c.postRound = true
}

// endPostRound closes the window after a round's end: the snapshot taken at the end is refreshed so
// it includes what happened after it (a later rewind restores the true totals).
func (c *Collector) endPostRound() {
	if !c.postRound {
		return
	}
	c.postRound = false
	c.cur = nil
	if n := len(c.snapshots); n > 0 {
		c.snapshots = c.snapshots[:n-1]
		c.snapshot()
	}
}

// teamAcc accumulates one team across the match.
type teamAcc struct {
	name    string
	players map[string]bool
}

// sideTeams works out which of the two teams is on CT this round, records who is on each and the
// in-game names, and returns the CT team's index. Teams are told apart by who is on them, not by
// side (sides swap at half time) and not by name (a server may set none).
func (c *Collector) sideTeams() int {
	gs := c.p.GameState()
	ids := func(ts *common.TeamState) []string {
		out := []string{}
		for _, pl := range ts.Members() {
			if pl != nil && pl.SteamID64 != 0 && !isCoach(pl) {
				out = append(out, strconv.FormatUint(pl.SteamID64, 10))
			}
		}
		return out
	}
	ct, t := gs.TeamCounterTerrorists(), gs.TeamTerrorists()
	ctIdx := c.assignTeams(ids(ct), ids(t))
	if n := ct.ClanName(); n != "" {
		c.teams[ctIdx].name = n
	}
	if n := t.ClanName(); n != "" {
		c.teams[1-ctIdx].name = n
	}
	return ctIdx
}

// assignTeams returns the index of the team the CT players belong to (the T players are the other
// one) and adds both rosters to their team. The first round founds the two teams.
func (c *Collector) assignTeams(ctIDs, tIDs []string) int {
	if len(c.teams) < 2 {
		c.teams = []*teamAcc{{players: map[string]bool{}}, {players: map[string]bool{}}}
	}
	overlap := func(team *teamAcc, ids []string) int {
		n := 0
		for _, id := range ids {
			if team.players[id] {
				n++
			}
		}
		return n
	}
	// CT is whichever team shares more players with this CT side, or with the other team's T side.
	score0 := overlap(c.teams[0], ctIDs) + overlap(c.teams[1], tIDs)
	score1 := overlap(c.teams[1], ctIDs) + overlap(c.teams[0], tIDs)
	ctIdx := 0
	if score1 > score0 {
		ctIdx = 1
	}
	for _, id := range ctIDs {
		c.teams[ctIdx].players[id] = true
	}
	for _, id := range tIDs {
		c.teams[1-ctIdx].players[id] = true
	}
	return ctIdx
}

// teamResults turns the accumulated teams into output, with scores counted from the kept rounds.
func (c *Collector) teamResults(gameScore func(i int) int) []Team {
	out := make([]Team, 0, len(c.teams))
	for i, acc := range c.teams {
		players := make([]string, 0, len(acc.players))
		for id := range acc.players {
			players = append(players, id)
		}
		sort.Strings(players)
		won := 0
		for _, r := range c.rounds {
			if (r.Winner == "CT") == (r.CTTeam == i) && r.Winner != "" {
				won++
			}
		}
		out = append(out, Team{Name: acc.name, Players: players, Score: won, GameScore: gameScore(i)})
	}
	return out
}

// Fires at the end of a Valve matchmaking match, once per player whose rank moved.
func (c *Collector) onRankUpdate(e events.RankUpdate) {
	x := c.playerByID(strconv.FormatUint(e.SteamID64(), 10))
	if x == nil {
		return
	}
	x.RankNew, x.RankChange = e.RankNew, e.RankChange
	if e.RankOld > 0 && x.Rank == 0 {
		x.Rank = e.RankOld
	}
	if e.WinCount > 0 {
		x.Wins = e.WinCount
	}
}

func (c *Collector) playerByID(id string) *Player {
	for _, x := range c.players {
		if x.SteamID == id {
			return x
		}
	}
	return nil
}

func (c *Collector) Rounds() []Round { return c.rounds }

func (c *Collector) Result(mapName string) Result {
	// Everyone, the departed included: when the match ends on its last kill, the game names that
	// round's MVP only after FACEIT has kicked the players (7 of 17 FACEIT maps), so it is on their
	// controllers and nowhere else.
	c.creditMVP(c.everyone())
	c.flushWorld()
	c.endPostRound()
	// A restart with no round after it (the demo ends first): judge it on the final score.
	c.settleRestart(c.p.GameState().TotalRoundsPlayed())
	if len(c.rounds) > 0 && !c.pendingRestart && !c.segmentStart {
		c.commitRounds()
		c.selfCheck(c.playing())
		c.check.finalMVPs(c.everyone(), logTotals(c.rounds))
	}
	n := len(c.rounds)
	out := make([]*Player, 0, len(c.players))
	for _, x := range c.players {
		if n > 0 {
			x.ADR = float64(x.Damage) / float64(n)
			x.KAST = float64(x.kastRounds) / float64(n)
			x.Rating = Rating2Approx(x.K, x.D, x.A, n, x.KAST, x.ADR)
		}
		out = append(out, x)
	}
	// The game's final score for each team: read off whichever side holds that team at the end.
	gs := c.p.GameState()
	gameScore := func(i int) int {
		if len(c.rounds) == 0 {
			return 0
		}
		last := c.rounds[len(c.rounds)-1]
		if last.CTTeam == i {
			return gs.TeamCounterTerrorists().Score()
		}
		return gs.TeamTerrorists().Score()
	}
	teams := c.teamResults(gameScore)
	sc := c.checkResult()
	if sc == nil { // a demo without the counters still gets its invariants checked
		sc = &SelfCheck{OK: true, Stats: []StatCheck{}, Diffs: []CheckDiff{}}
	}
	if sc.Broken = checkInvariants(c.rounds, teams); len(sc.Broken) > 0 {
		sc.OK = false
	}
	return Result{LogVersion: LogVersion, Mode: c.mode(), Map: mapName, TickRate: c.p.TickRate(), Rounds: c.rounds, Players: out, Restarts: c.restarts, Restores: c.restores, Teams: teams, SelfCheck: sc}
}

// mode reads the rank type the players were queued under. Empty when the demo did not come from a
// Valve server (FACEIT, ESEA and LAN demos carry no rank type at all).
func (c *Collector) mode() string {
	counts := map[int]int{}
	for _, x := range c.players {
		if x.RankType > 0 {
			counts[x.RankType]++
		}
	}
	best, bestN := 0, 0
	for t, n := range counts {
		if n > bestN {
			best, bestN = t, n
		}
	}
	switch best {
	case 11:
		return ModePremier
	case 12:
		return ModeCompetitive
	case 7:
		return ModeWingman
	default:
		return ""
	}
}

// playing is everyone on T or CT who plays: a coach is on the team (and, to the library, alive) but
// never plays, and would otherwise get a stat line, a roster spot and keep their side "alive" in
// every clutch.
func (c *Collector) playing() []*common.Player {
	all := c.p.GameState().Participants().Playing()
	out := all[:0:0]
	for _, pl := range all {
		if !isCoach(pl) {
			out = append(out, pl)
		}
	}
	return out
}

func isCoach(pl *common.Player) bool {
	if pl == nil || pl.Entity == nil {
		return false
	}
	v, ok := pl.Entity.PropertyValue("m_iCoachingTeam")
	return ok && v.Any != nil && v.Int() != 0
}

// everyone: every player with a stat line whose controller is still there, including players who
// left. Only for what the game settles after the match (the final round's MVP): a departed
// controller also collects deaths from the match-end slay, which were never played.
func (c *Collector) everyone() []*common.Player {
	out := []*common.Player{}
	for _, pl := range c.p.GameState().Participants().All() {
		if pl != nil && pl.SteamID64 != 0 && pl.Entity != nil && !isCoach(pl) && c.players[pl.SteamID64] != nil {
			out = append(out, pl)
		}
	}
	return out
}
