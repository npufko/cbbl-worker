// Package stats turns demoinfocs events into cbbl's per-map JSON (plan §6 MapResult + match_rounds).
package stats

import (
	"strconv"

	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

const (
	tradeWindowSeconds = 5.0
	minFlashSeconds    = 1.1 // ignore trivial flashes
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
}

type Clutch struct {
	SteamID string `json:"steamId"`
	Vs      int    `json:"vs"`
	Won     bool   `json:"won"`
}

type Round struct {
	N      int     `json:"n"`
	Winner string  `json:"winner"` // "CT" | "T"; mapped to series sides by the worker using team rosters
	Reason int     `json:"reason"` // events.RoundEndReason
	Equip  [2]int  `json:"equip"`  // [CT, T] freeze-time-end equipment value
	Kills  []Kill  `json:"kills"`
	Clutch *Clutch `json:"clutch,omitempty"`
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
	kastRounds     int
}

type Result struct {
	Map      string    `json:"map"`
	TickRate float64   `json:"tickrate"`
	Rounds   []Round   `json:"rounds"`
	Players  []*Player `json:"players"`
}

type Collector struct {
	p          dem.Parser
	rounds     []Round
	players    map[uint64]*Player
	cur        *Round
	roundStart float64
	// per-round state
	contributed map[uint64]bool // KAST: kill, assist or traded this round
	died        map[uint64]float64
	killerOf    map[uint64]uint64
	clutch      map[common.Team]*Clutch
}

func NewCollector(p dem.Parser) *Collector {
	c := &Collector{p: p, players: map[uint64]*Player{}}
	p.RegisterEventHandler(c.onRoundStart)
	p.RegisterEventHandler(c.onFreezeEnd)
	p.RegisterEventHandler(c.onKill)
	p.RegisterEventHandler(c.onHurt)
	p.RegisterEventHandler(c.onFlashed)
	p.RegisterEventHandler(c.onRoundEnd)
	return c
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
	if !c.live() {
		return
	}
	c.cur = &Round{N: len(c.rounds) + 1, Kills: []Kill{}}
	c.roundStart = c.now()
	c.contributed = map[uint64]bool{}
	c.died = map[uint64]float64{}
	c.killerOf = map[uint64]uint64{}
	c.clutch = map[common.Team]*Clutch{}
}

func (c *Collector) onFreezeEnd(events.RoundFreezetimeEnd) {
	if c.cur == nil {
		return
	}
	for _, pl := range c.p.GameState().Participants().Playing() {
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
	k := Kill{T: t - c.roundStart, Victim: strconv.FormatUint(e.Victim.SteamID64, 10), HS: e.IsHeadshot, Opening: len(c.cur.Kills) == 0}
	if e.Weapon != nil {
		k.Weapon = e.Weapon.String()
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
	for _, pl := range c.p.GameState().Participants().Playing() {
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

func (c *Collector) onHurt(e events.PlayerHurt) {
	if c.cur == nil || e.Attacker == nil || e.Player == nil || e.Attacker.Team == e.Player.Team {
		return
	}
	a := c.player(e.Attacker)
	if a == nil {
		return
	}
	a.Damage += e.HealthDamageTaken
	if e.Weapon != nil && e.Weapon.Class() == common.EqClassGrenade {
		a.UtilDmg += e.HealthDamageTaken
	}
}

func (c *Collector) onFlashed(e events.PlayerFlashed) {
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
	if c.cur == nil {
		return
	}
	c.cur.Reason = int(e.Reason)
	c.cur.Winner = map[common.Team]string{common.TeamCounterTerrorists: "CT", common.TeamTerrorists: "T"}[e.Winner]
	// KAST survival: anyone playing who did not die this round.
	for _, pl := range c.p.GameState().Participants().Playing() {
		if _, dead := c.died[pl.SteamID64]; !dead {
			c.contributed[pl.SteamID64] = true
		}
	}
	for id := range c.contributed {
		if x := c.players[id]; x != nil {
			x.kastRounds++
		}
	}
	// Ensure every participant exists even with zero events.
	for _, pl := range c.p.GameState().Participants().Playing() {
		c.player(pl)
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
	c.rounds = append(c.rounds, *c.cur)
	c.cur = nil
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
	return Result{Map: mapName, TickRate: c.p.TickRate(), Rounds: c.rounds, Players: out}
}
