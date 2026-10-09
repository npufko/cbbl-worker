package replay

import (
	"fmt"
	"sort"
)

// Highlight is one player's best round, with the clip window to render.
type Highlight struct {
	SteamID   uint64  `json:"steamId,string"`
	Name      string  `json:"name"`
	Round     int     `json:"round"`
	Kills     int     `json:"kills"`
	HS        int     `json:"hs"`
	ClutchVs  int     `json:"clutchVs,omitempty"` // >0 when the player won a 1vN
	Score     float64 `json:"score"`
	Label     string  `json:"label"` // "4K + 1v2 clutch"
	FromTick  int     `json:"fromTick"`
	ToTick    int     `json:"toTick"`
	roundRef  *Round
}

const (
	leadSeconds = 4.0  // context before the first kill
	tailSeconds = 2.0  // after the last kill
	maxSeconds  = 30.0 // Discord-friendly length cap
)

// Highlights returns every player's best round, best first. Scoring rewards multi-kills
// super-linearly (1K=1 … 5K=25), won clutches (6 per opponent), and headshots (0.5 each).
func Highlights(rec *Recording) []Highlight {
	best := map[uint64]Highlight{}
	for _, r := range rec.Rounds {
		if len(r.Frames) == 0 {
			continue
		}
		clutch := clutches(r)
		byPlayer := map[uint64][]Kill{}
		for _, k := range r.Kills {
			if k.Killer != 0 && k.KillerCT != k.VictimCT { // no team kills
				byPlayer[k.Killer] = append(byPlayer[k.Killer], k)
			}
		}
		for id, ks := range byPlayer {
			hs := 0
			for _, k := range ks {
				if k.HS {
					hs++
				}
			}
			vs := clutch[id]
			score := float64(len(ks)*len(ks)) + 6*float64(vs) + 0.5*float64(hs)
			if cur, ok := best[id]; ok && cur.Score >= score {
				continue
			}
			from, to := window(rec, r, ks, vs > 0)
			best[id] = Highlight{
				SteamID: id, Name: ks[0].KillerName, Round: r.N, Kills: len(ks), HS: hs, ClutchVs: vs,
				Score: score, Label: label(len(ks), vs), FromTick: from, ToTick: to, roundRef: r,
			}
		}
	}
	out := make([]Highlight, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// clutches returns steamID -> N for players who became their side's last one alive against N and won.
func clutches(r *Round) map[uint64]int {
	alive := map[uint64]bool{}
	ct := map[uint64]bool{}
	for _, p := range r.Frames[0].Players {
		if p.Alive {
			alive[p.ID] = true
		}
		ct[p.ID] = p.CT
	}
	count := func(side bool) (n int, last uint64) {
		for id, a := range alive {
			if a && ct[id] == side {
				n++
				last = id
			}
		}
		return
	}
	started := map[uint64]int{}
	for _, k := range r.Kills {
		alive[k.Victim] = false
		for _, side := range []bool{true, false} {
			n, last := count(side)
			enemies, _ := count(!side)
			if n == 1 && enemies >= 1 {
				if _, ok := started[last]; !ok {
					started[last] = enemies
				}
			}
		}
	}
	won := map[uint64]int{}
	for id, vs := range started {
		if ct[id] == r.CTWon {
			won[id] = vs
		}
	}
	return won
}

func window(rec *Recording, r *Round, ks []Kill, clutch bool) (int, int) {
	tr := rec.TickRate
	if tr <= 0 {
		tr = 64
	}
	from := ks[0].Tick - int(leadSeconds*tr)
	to := ks[len(ks)-1].Tick + int(tailSeconds*tr)
	if clutch && r.EndTick > to {
		to = r.EndTick
	}
	if from < r.StartTick {
		from = r.StartTick
	}
	if to > r.EndTick && r.EndTick > 0 {
		to = r.EndTick
	}
	if max := int(maxSeconds * tr); to-from > max {
		from = to - max
	}
	return from, to
}

func label(kills, vs int) string {
	s := fmt.Sprintf("%dK", kills)
	if kills == 5 {
		s = "ACE"
	}
	if vs > 0 {
		s += fmt.Sprintf(" + 1v%d clutch", vs)
	}
	return s
}
