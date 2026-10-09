// Package replay records what a 2D highlight needs: sampled player positions and view angles,
// kills, and round windows. It runs in the same single parse pass as the stats collector.
package replay

import (
	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

type PlayerState struct {
	ID    uint64
	Name  string
	CT    bool
	X, Y  float64
	Yaw   float64 // degrees
	Alive bool
}

type Frame struct {
	Tick    int
	Players []PlayerState
}

type Kill struct {
	Tick                   int
	Killer, Victim         uint64
	KillerName, VictimName string
	KillerCT, VictimCT     bool
	Weapon                 string
	HS                     bool
}

type Round struct {
	N                  int
	StartTick, EndTick int
	CTWon              bool
	Kills              []Kill
	Frames             []Frame
}

type Recording struct {
	Map      string
	TickRate float64
	Rounds   []*Round
	Density  [][2]float64 // sparse positions over the whole match: the map silhouette, drawn from data (no game assets)
}

type Recorder struct {
	p     dem.Parser
	every int
	rec   Recording
	cur   *Round
	n     int
}

// NewRecorder samples every `every` ticks (2 at 64-tick = 32 samples/s = one video frame per sample).
func NewRecorder(p dem.Parser, every int) *Recorder {
	r := &Recorder{p: p, every: every}
	// Same restart rule as the stats collector: a knife round is played, then the game restarts.
	// Without this, a 7-kill knife round scores as an ace and becomes "Play of the match", and every
	// round number in the clip caption is one too high.
	p.RegisterEventHandler(func(events.MatchStart) { r.reset() })
	p.RegisterEventHandler(func(events.RoundFreezetimeEnd) {
		if !r.live() {
			return
		}
		r.n++
		r.cur = &Round{N: r.n, StartTick: p.GameState().IngameTick()}
	})
	p.RegisterEventHandler(func(e events.Kill) {
		if r.cur == nil || e.Victim == nil {
			return
		}
		k := Kill{Tick: p.GameState().IngameTick(), Victim: e.Victim.SteamID64, VictimName: e.Victim.Name, HS: e.IsHeadshot,
			VictimCT: e.Victim.Team == common.TeamCounterTerrorists}
		if e.Killer != nil {
			k.Killer, k.KillerName, k.KillerCT = e.Killer.SteamID64, e.Killer.Name, e.Killer.Team == common.TeamCounterTerrorists
		}
		if e.Weapon != nil {
			k.Weapon = e.Weapon.String()
		}
		r.cur.Kills = append(r.cur.Kills, k)
	})
	p.RegisterEventHandler(func(e events.RoundEnd) {
		if r.cur == nil {
			return
		}
		r.cur.EndTick = p.GameState().IngameTick()
		r.cur.CTWon = e.Winner == common.TeamCounterTerrorists
		r.rec.Rounds = append(r.rec.Rounds, r.cur)
		r.cur = nil
	})
	p.RegisterEventHandler(func(events.FrameDone) { r.sample() })
	return r
}

// reset drops rounds collected before a (re)start. Density is kept: it is only the map silhouette
// drawn from where players walked, so earlier samples are still good data.
func (r *Recorder) reset() {
	r.rec.Rounds = nil
	r.cur = nil
	r.n = 0
}

func (r *Recorder) live() bool {
	gs := r.p.GameState()
	return gs.IsMatchStarted() && !gs.IsWarmupPeriod()
}

func (r *Recorder) sample() {
	tick := r.p.GameState().IngameTick()
	playing := r.p.GameState().Participants().Playing()
	if tick%64 == 0 && len(r.rec.Density) < 200_000 {
		for _, pl := range playing {
			if pl.IsAlive() {
				pos := pl.Position()
				r.rec.Density = append(r.rec.Density, [2]float64{pos.X, pos.Y})
			}
		}
	}
	if r.cur == nil || tick%r.every != 0 {
		return
	}
	f := Frame{Tick: tick, Players: make([]PlayerState, 0, len(playing))}
	for _, pl := range playing {
		if pl.SteamID64 == 0 || (pl.Team != common.TeamCounterTerrorists && pl.Team != common.TeamTerrorists) {
			continue
		}
		pos := pl.Position()
		f.Players = append(f.Players, PlayerState{
			ID: pl.SteamID64, Name: pl.Name, CT: pl.Team == common.TeamCounterTerrorists,
			X: pos.X, Y: pos.Y, Yaw: float64(pl.ViewDirectionX()), Alive: pl.IsAlive(),
		})
	}
	r.cur.Frames = append(r.cur.Frames, f)
}

func (r *Recorder) Result(mapName string) *Recording {
	r.rec.Map = mapName
	r.rec.TickRate = r.p.TickRate()
	return &r.rec
}
