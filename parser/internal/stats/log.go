package stats

import (
	"math"
	"strconv"

	"github.com/golang/geo/r3"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

// The event log: facts, not formulas. Every stat and rating cbbl shows is a pure function of these
// facts (packages/core), so a stat or rating shipped later can be computed for every map parsed
// since, without the demo. Everything here lives inside its Round, so a restart, a round-backup
// restore or a replayed round drops it along with the round.
//
// LogVersion goes up whenever a fact is added or its meaning changes; a stat declares the version it
// needs and reads "—" on older maps rather than guessing.
// v3: damage is the health each hit actually took (hpTaken), and round MVPs come from the scoreboard
// counters when the demo has no announcement (FACEIT). v2 logs undercount damage by up to 1 HP a hit.
// v4: Damage.Impact marks a grenade striking a player in flight (1-2 HP). The game counts it as
// damage but not as utility damage; v3 and older logs count it as both. Also: a killing hit in the
// same tick as another hit on the victim no longer counts that hit's health twice, and coaches are
// not players (no line, no round entry, not alive in a clutch).
// v5: fire hits missing from the demo, inferred from health (Damage.Inferred); a player dying alone
// to "World" after the round (a fall) is a death, as the game counts it.
const LogVersion = 5

// v2: kills, damage, blinds and grenades between a round's end and the next round's start count for
// the round that just ended (v1 dropped them: FACEIT and HLTV count exit frags).

const (
	sightFOVDegrees  = 50.0 // half-angle of the view cone an enemy must be in to count as seen
	sightLostSeconds = 1.0  // unseen this long ends a sighting
	sprayGapSeconds  = 0.2  // shots closer than this belong to one burst (a rifle cycles in ~0.1 s); 3+ make a spray
	stillSpeedRatio  = 0.34 // Leetify's counter-strafe line: below 34% of the weapon's max speed
)

// RoundPlayer is one player's state in a round: loadout at freeze-time end, outcome at the end.
type RoundPlayer struct {
	SteamID string `json:"steamId"`
	Side    string `json:"side"`    // "CT" | "T"
	Equip   int    `json:"equip"`   // equipment value at freeze-time end (after buys)
	Money   int    `json:"money"`   // money left at freeze-time end
	Spent   int    `json:"spent"`   // money spent this round, at freeze-time end
	Armor   int    `json:"armor"`   // armor points at freeze-time end
	Helmet  bool   `json:"helmet"`  //
	Kit     bool   `json:"kit"`     // defuse kit
	Primary string `json:"primary"` // best gun held at freeze-time end ("" = pistol round / no rifle)
	HP      int    `json:"hp"`      // health at the round's end (0 = dead)
	// Value of grenades still held when the player died or the round ended.
	UtilLeft int `json:"utilLeft"`
}

// Damage is one hit. T is seconds since the round started (as Kill.T).
type Damage struct {
	T        float64 `json:"t"`
	Attacker string  `json:"a,omitempty"` // "" = world (fall, bomb)
	Victim   string  `json:"v"`
	HP       int     `json:"hp"` // health taken, over-damage excluded
	Armor    int     `json:"ar"`
	Weapon   string  `json:"w"`
	HitGroup int     `json:"hg"` // events.HitGroup: 1 head, 2 chest, 3 stomach, 4-7 limbs, 0 generic
	// A grenade hitting the victim in flight, not its explosion or fire (see isImpact).
	Impact bool `json:"impact,omitempty"`
	// A hit the demo has no hurt event for, read from the victim's health (see onHealthFrame).
	Inferred bool `json:"inferred,omitempty"`
}

// Nade is one grenade: thrown and, when it went off, where and when.
type Nade struct {
	ID     int     `json:"id"` // entity id, joins Blind.Nade
	By     string  `json:"by"`
	Type   string  `json:"type"` // flash | smoke | he | molotov | incendiary | decoy
	T      float64 `json:"t"`
	From   [3]int  `json:"from"`
	DetT   float64 `json:"detT,omitempty"`
	DetPos *[3]int `json:"det,omitempty"`
	Around []At    `json:"around,omitempty"` // smokes: everyone alive when it landed
}

// At is where one living player stood at a moment (a kill, a smoke landing).
type At struct {
	SteamID string `json:"id"`
	Pos     [3]int `json:"p"`
	HP      int    `json:"hp"`
}

// around: every living player at this instant, except those named.
func (c *Collector) around(except ...*common.Player) []At {
	out := []At{}
	for _, pl := range c.playing() {
		if !pl.IsAlive() || pl.SteamID64 == 0 {
			continue
		}
		skip := false
		for _, x := range except {
			if x != nil && x.SteamID64 == pl.SteamID64 {
				skip = true
			}
		}
		if !skip {
			out = append(out, At{SteamID: sid(pl), Pos: vec3(pl.Position()), HP: pl.Health()})
		}
	}
	return out
}

// Blind is one player blinded by a flash.
type Blind struct {
	T        float64 `json:"t"`
	By       string  `json:"by"`
	Victim   string  `json:"v"`
	Seconds  float64 `json:"s"`
	Teammate bool    `json:"team"`
	Nade     int     `json:"nade,omitempty"`
}

// Shots counts one player's fire with one weapon in a round.
type Shots struct {
	SteamID     string `json:"steamId"`
	Weapon      string `json:"w"`
	Fired       int    `json:"fired"`
	Hits        int    `json:"hits"`    // hurt events from this weapon
	Head        int    `json:"head"`    // of those, to the head
	Spotted     int    `json:"spotted"` // shots fired while an enemy was in view
	SpottedHits int    `json:"spottedHits"`
	Spray       int    `json:"spray"`     // shots that were part of a 3+ shot burst
	SprayHits   int    `json:"sprayHits"` //
	Still       int    `json:"still"`     // rifle shots below 34% of max speed, not crouched
	Moving      int    `json:"moving"`    // rifle shots at or above it (Still + Moving = rifle shots)
}

// Sighting is one aim sample: an enemy came into view, and this player then damaged them.
type Sighting struct {
	Shooter string  `json:"s"`
	Victim  string  `json:"v"`
	T       float64 `json:"t"`   // when first seen, seconds since round start
	TTD     float64 `json:"ttd"` // seconds from seen to first damage
	Angle   float64 `json:"ang"` // degrees between crosshair and the enemy's head when first seen
	Moved   float64 `json:"mv"`  // degrees the crosshair travelled from first seen to first damage
	Weapon  string  `json:"w"`
}

// Bomb is the round's bomb story.
type Bomb struct {
	Site     string  `json:"site,omitempty"`
	Planter  string  `json:"planter,omitempty"`
	PlantT   float64 `json:"plantT,omitempty"`
	Defuser  string  `json:"defuser,omitempty"`
	DefuseT  float64 `json:"defuseT,omitempty"`
	Exploded bool    `json:"exploded,omitempty"`
}

type sighting struct {
	start, lastSeen float64
	angle           float64
	view0           r3.Vector
	damaged         bool
}

type burst struct {
	last  float64
	count int
}

type posSample struct {
	t    float64
	x, y float64
}

// logState is per-round working state for the log, reset at every round start and void.
type logState struct {
	sight   map[[2]uint64]*sighting
	bursts  map[uint64]*burst
	nadeIdx map[int]int
	shotIdx map[string]int
	seeing  map[uint64]int // enemies in view, per player, as of the last frame
}

func newLogState() logState {
	return logState{sight: map[[2]uint64]*sighting{}, bursts: map[uint64]*burst{}, nadeIdx: map[int]int{}, shotIdx: map[string]int{}, seeing: map[uint64]int{}}
}

func r2(v float64) float64 { return math.Round(v*100) / 100 }
func r1(v float64) float64 { return math.Round(v*10) / 10 }
func sid(pl *common.Player) string {
	if pl == nil || pl.SteamID64 == 0 {
		return ""
	}
	return strconv.FormatUint(pl.SteamID64, 10)
}
func vec3(v r3.Vector) [3]int {
	return [3]int{int(math.Round(v.X)), int(math.Round(v.Y)), int(math.Round(v.Z))}
}
func sideOf(t common.Team) string {
	switch t {
	case common.TeamCounterTerrorists:
		return "CT"
	case common.TeamTerrorists:
		return "T"
	}
	return ""
}

// viewVec is the unit vector a player looks along. Pitch is stored 0..360 with down positive.
func viewVec(pl *common.Player) r3.Vector {
	yaw := float64(pl.ViewDirectionX()) * math.Pi / 180
	p := float64(pl.ViewDirectionY())
	if p > 180 {
		p -= 360
	}
	pitch := p * math.Pi / 180
	return r3.Vector{X: math.Cos(pitch) * math.Cos(yaw), Y: math.Cos(pitch) * math.Sin(yaw), Z: -math.Sin(pitch)}
}

func angleBetween(a, b r3.Vector) float64 {
	n := a.Norm() * b.Norm()
	if n == 0 {
		return 180
	}
	c := a.Dot(b) / n
	return math.Acos(math.Max(-1, math.Min(1, c))) * 180 / math.Pi
}

// angleTo: degrees between where the shooter looks and the target's head.
func angleTo(shooter, target *common.Player) float64 {
	from, ok1 := shooter.PositionEyes()
	to, ok2 := target.PositionEyes()
	if !ok1 || !ok2 {
		return 180
	}
	return angleBetween(viewVec(shooter), to.Sub(from))
}

// Max running speed per rifle (units/s), for the counter-strafe line.
var rifleMaxSpeed = map[common.EquipmentType]float64{
	common.EqAK47: 215, common.EqM4A4: 225, common.EqM4A1: 225, common.EqFamas: 220, common.EqGalil: 215,
	common.EqAUG: 220, common.EqSG553: 210, common.EqAWP: 200, common.EqSSG08: 230, common.EqScar20: 215, common.EqG3SG1: 215,
}

var utilValue = map[common.EquipmentType]int{
	common.EqFlash: 200, common.EqSmoke: 300, common.EqHE: 300, common.EqMolotov: 400, common.EqIncendiary: 500, common.EqDecoy: 50,
}

var nadeName = map[common.EquipmentType]string{
	common.EqFlash: "flash", common.EqSmoke: "smoke", common.EqHE: "he", common.EqMolotov: "molotov", common.EqIncendiary: "incendiary", common.EqDecoy: "decoy",
}

func utilHeld(pl *common.Player) int {
	v := 0
	for _, w := range pl.Weapons() {
		if w != nil {
			v += utilValue[w.Type]
		}
	}
	return v
}

func (c *Collector) roundT() float64 { return r2(c.now() - c.roundStart) }

// roundPlayer finds or adds this round's entry for a player.
func (c *Collector) roundPlayer(pl *common.Player) *RoundPlayer {
	id := sid(pl)
	if id == "" || c.cur == nil {
		return nil
	}
	for i := range c.cur.Players {
		if c.cur.Players[i].SteamID == id {
			return &c.cur.Players[i]
		}
	}
	c.cur.Players = append(c.cur.Players, RoundPlayer{SteamID: id, Side: sideOf(pl.Team)})
	return &c.cur.Players[len(c.cur.Players)-1]
}

// logFreezeEnd records every player's loadout once buys are done.
func (c *Collector) logFreezeEnd() {
	for _, pl := range c.playing() {
		rp := c.roundPlayer(pl)
		if rp == nil {
			continue
		}
		rp.Side = sideOf(pl.Team)
		rp.Equip = pl.EquipmentValueFreezeTimeEnd()
		rp.Money = pl.Money()
		rp.Spent = pl.MoneySpentThisRound()
		rp.Armor = pl.Armor()
		rp.Helmet = pl.HasHelmet()
		rp.Kit = pl.HasDefuseKit()
		best := 0
		for _, w := range pl.Weapons() {
			if w == nil {
				continue
			}
			rank := map[common.EquipmentClass]int{common.EqClassRifle: 3, common.EqClassSMG: 2, common.EqClassHeavy: 2}[w.Class()]
			if rank > best {
				best, rp.Primary = rank, w.String()
			}
		}
	}
}

// logRoundEnd records how everyone finished the round.
func (c *Collector) logRoundEnd() {
	for _, pl := range c.playing() {
		rp := c.roundPlayer(pl)
		if rp == nil {
			continue
		}
		if pl.IsAlive() {
			rp.HP = pl.Health()
			rp.UtilLeft = utilHeld(pl)
		}
	}
}

// onFrame: speed for counter-strafing, and who can see whom for aim samples. Runs every tick.
func (c *Collector) onFrame(events.FrameDone) {
	if c.cur == nil {
		return
	}
	now := c.now()
	playing := c.playing()
	for _, pl := range playing {
		if !pl.IsAlive() {
			continue
		}
		pos := pl.Position()
		if prev, ok := c.lastPos[pl.SteamID64]; ok && now > prev.t {
			c.speed[pl.SteamID64] = math.Hypot(pos.X-prev.x, pos.Y-prev.y) / (now - prev.t)
		}
		c.lastPos[pl.SteamID64] = posSample{t: now, x: pos.X, y: pos.Y}
	}
	for _, s := range playing {
		if !s.IsAlive() || s.SteamID64 == 0 {
			continue
		}
		seeing := 0
		for _, v := range playing {
			if v == s || !v.IsAlive() || v.Team == s.Team || v.SteamID64 == 0 {
				continue
			}
			key := [2]uint64{s.SteamID64, v.SteamID64}
			st := c.log.sight[key]
			ang := angleTo(s, v)
			if v.IsSpottedBy(s) && ang <= sightFOVDegrees {
				seeing++
				if st == nil {
					st = &sighting{start: now, angle: ang, view0: viewVec(s)}
					c.log.sight[key] = st
				}
				st.lastSeen = now
			} else if st != nil && now-st.lastSeen > sightLostSeconds {
				delete(c.log.sight, key)
			}
		}
		c.log.seeing[s.SteamID64] = seeing
	}
}

func (c *Collector) shots(pl *common.Player, w *common.Equipment) *Shots {
	key := sid(pl) + "|" + w.String()
	if i, ok := c.log.shotIdx[key]; ok {
		return &c.cur.Shots[i]
	}
	c.cur.Shots = append(c.cur.Shots, Shots{SteamID: sid(pl), Weapon: w.String()})
	c.log.shotIdx[key] = len(c.cur.Shots) - 1
	return &c.cur.Shots[len(c.cur.Shots)-1]
}

func isGun(w *common.Equipment) bool {
	if w == nil {
		return false
	}
	switch w.Class() {
	case common.EqClassPistols, common.EqClassSMG, common.EqClassHeavy, common.EqClassRifle:
		return true
	}
	return false
}

func (c *Collector) onFire(e events.WeaponFire) {
	if c.cur == nil || e.Shooter == nil || e.Shooter.SteamID64 == 0 || !isGun(e.Weapon) {
		return
	}
	now := c.now()
	s := c.shots(e.Shooter, e.Weapon)
	s.Fired++
	if c.log.seeing[e.Shooter.SteamID64] > 0 {
		s.Spotted++
	}
	b := c.log.bursts[e.Shooter.SteamID64]
	if b == nil || now-b.last > sprayGapSeconds {
		b = &burst{}
		c.log.bursts[e.Shooter.SteamID64] = b
	}
	b.count++
	b.last = now
	switch {
	case b.count == 3:
		s.Spray += 3 // the burst just became a spray: its first two shots count too
	case b.count > 3:
		s.Spray++
	}
	if max, ok := rifleMaxSpeed[e.Weapon.Type]; ok {
		if c.speed[e.Shooter.SteamID64] < stillSpeedRatio*max && !e.Shooter.IsDucking() {
			s.Still++
		} else {
			s.Moving++
		}
	}
}

// logHurt records the hit, gun accuracy, and closes an aim sample on the first damage of a sighting.
func (c *Collector) logHurt(e events.PlayerHurt, hp int) {
	if c.cur == nil || e.Player == nil {
		return
	}
	w := ""
	if e.Weapon != nil {
		w = e.Weapon.String()
	}
	c.cur.Damage = append(c.cur.Damage, Damage{T: c.roundT(), Attacker: sid(e.Attacker), Victim: sid(e.Player),
		HP: hp, Armor: e.ArmorDamageTaken, Weapon: w, HitGroup: int(e.HitGroup), Impact: c.isImpact(e, hp)})
	if e.Attacker == nil || e.Attacker.Team == e.Player.Team || !isGun(e.Weapon) {
		return
	}
	s := c.shots(e.Attacker, e.Weapon)
	s.Hits++
	if e.HitGroup == events.HitGroupHead {
		s.Head++
	}
	if c.log.seeing[e.Attacker.SteamID64] > 0 {
		s.SpottedHits++
	}
	if b := c.log.bursts[e.Attacker.SteamID64]; b != nil && b.count >= 3 {
		s.SprayHits++
	}
	if st := c.log.sight[[2]uint64{e.Attacker.SteamID64, e.Player.SteamID64}]; st != nil && !st.damaged {
		st.damaged = true
		now := c.now()
		c.cur.Sightings = append(c.cur.Sightings, Sighting{
			Shooter: sid(e.Attacker), Victim: sid(e.Player), T: r2(st.start - c.roundStart), TTD: r2(now - st.start),
			Angle: r1(st.angle), Moved: r1(angleBetween(st.view0, viewVec(e.Attacker))), Weapon: w,
		})
	}
}

// A grenade striking a player: how close (units, from the victim's mid-body; a player is ~72 tall,
// ~32 wide) the attacker's projectile must be, and the most health it takes (seen: 1-2 HP).
const (
	impactReach = 64.0
	impactMaxHP = 5
)

// isImpact: the hit came from a grenade of the attacker's that is still flying, touching the victim.
// Fire comes after a molotov's projectile is gone; an HE's projectile is still there on the tick it
// detonates, but a blast that close takes far more than an impact does.
func (c *Collector) isImpact(e events.PlayerHurt, hp int) bool {
	if hp > impactMaxHP || e.Weapon == nil || e.Weapon.Class() != common.EqClassGrenade || e.Attacker == nil || e.Player == nil {
		return false
	}
	body := e.Player.Position().Add(r3.Vector{Z: 36})
	for _, g := range c.p.GameState().GrenadeProjectiles() {
		if g == nil || g.Entity == nil || g.Thrower == nil {
			continue
		}
		if g.Thrower.SteamID64 != e.Attacker.SteamID64 || nadeKind(projectileType(g)) != nadeKind(e.Weapon.Type) {
			continue
		}
		if g.Position().Sub(body).Norm() <= impactReach {
			return true
		}
	}
	return false
}

// projectileType is the grenade a projectile is. The library knows it from the projectile's model,
// and a projectile can arrive without one ("unknown grenade model 0": an HE in a Premier match,
// dropped from the log). Its entity class names the grenade either way; a molotov-class projectile
// with no model is logged as a molotov (one kind with the incendiary, see nadeKind).
func projectileType(g *common.GrenadeProjectile) common.EquipmentType {
	if g.WeaponInstance != nil && g.WeaponInstance.Type != common.EqUnknown {
		return g.WeaponInstance.Type
	}
	if g.Entity == nil {
		return common.EqUnknown
	}
	switch g.Entity.ServerClass().Name() {
	case "CHEGrenadeProjectile":
		return common.EqHE
	case "CFlashbangProjectile":
		return common.EqFlash
	case "CSmokeGrenadeProjectile":
		return common.EqSmoke
	case "CMolotovProjectile":
		return common.EqMolotov
	case "CDecoyProjectile":
		return common.EqDecoy
	}
	return common.EqUnknown
}

// nadeKind: molotov and incendiary are one kind here. A T throwing a picked-up incendiary hits
// people as "Molotov" (Stx, 1-5b0b4db1 r15), so the hit and its projectile disagree on the name.
func nadeKind(t common.EquipmentType) common.EquipmentType {
	if t == common.EqIncendiary {
		return common.EqMolotov
	}
	return t
}

func (c *Collector) onThrow(e events.GrenadeProjectileThrow) {
	if c.cur == nil || e.Projectile == nil || e.Projectile.Entity == nil {
		return
	}
	name, ok := nadeName[projectileType(e.Projectile)]
	if !ok {
		return
	}
	id := 0
	if e.Projectile.Entity != nil {
		id = e.Projectile.Entity.ID()
	}
	c.cur.Nades = append(c.cur.Nades, Nade{ID: id, By: sid(e.Projectile.Thrower), Type: name, T: c.roundT(), From: vec3(e.Projectile.Position())})
	c.log.nadeIdx[id] = len(c.cur.Nades) - 1
}

func (c *Collector) onNadeEvent(e events.GrenadeEventIf) {
	if c.cur == nil {
		return
	}
	switch e.(type) {
	case events.HeExplode, events.FlashExplode, events.SmokeStart, events.FireGrenadeStart, events.DecoyStart:
	default:
		return
	}
	b := e.Base()
	i, ok := c.log.nadeIdx[b.GrenadeEntityID]
	if !ok || c.cur.Nades[i].DetT != 0 {
		return
	}
	pos := vec3(b.Position)
	c.cur.Nades[i].DetT = c.roundT()
	c.cur.Nades[i].DetPos = &pos
	if _, smoke := e.(events.SmokeStart); smoke {
		c.cur.Nades[i].Around = c.around()
	}
}

// onNadeDestroy: molotovs and incendiaries land when their projectile goes; CS2 demos do not
// reliably send their fire-start event, so this is where they "go off".
func (c *Collector) onNadeDestroy(e events.GrenadeProjectileDestroy) {
	if c.cur == nil || e.Projectile == nil || e.Projectile.Entity == nil {
		return
	}
	if t := projectileType(e.Projectile); t != common.EqMolotov && t != common.EqIncendiary {
		return
	}
	i, ok := c.log.nadeIdx[e.Projectile.Entity.ID()]
	if !ok || c.cur.Nades[i].DetT != 0 {
		return
	}
	pos := vec3(e.Projectile.Position())
	c.cur.Nades[i].DetT = c.roundT()
	c.cur.Nades[i].DetPos = &pos
}

func (c *Collector) logBlind(e events.PlayerFlashed) {
	if c.cur == nil || e.Player == nil || e.Attacker == nil {
		return
	}
	nade := 0
	if e.Projectile != nil && e.Projectile.Entity != nil {
		nade = e.Projectile.Entity.ID()
	}
	c.cur.Blinds = append(c.cur.Blinds, Blind{T: c.roundT(), By: sid(e.Attacker), Victim: sid(e.Player),
		Seconds: r2(e.FlashDuration().Seconds()), Teammate: e.Attacker.Team == e.Player.Team, Nade: nade})
}

func (c *Collector) onPlanted(e events.BombPlanted) {
	if c.cur == nil {
		return
	}
	c.cur.Bomb = &Bomb{Site: string(rune(e.Site)), Planter: sid(e.Player), PlantT: c.roundT()}
	if e.Site == 0 {
		c.cur.Bomb.Site = ""
	}
}

func (c *Collector) onDefused(e events.BombDefused) {
	if c.cur == nil {
		return
	}
	if c.cur.Bomb == nil {
		c.cur.Bomb = &Bomb{}
	}
	c.cur.Bomb.Defuser, c.cur.Bomb.DefuseT = sid(e.Player), c.roundT()
}

func (c *Collector) onExploded(events.BombExplode) {
	if c.cur == nil {
		return
	}
	if c.cur.Bomb == nil {
		c.cur.Bomb = &Bomb{}
	}
	c.cur.Bomb.Exploded = true
}

// onMVP: the announcement comes after the round ended, so it goes to the round just completed.
func (c *Collector) onMVP(e events.RoundMVPAnnouncement) {
	if len(c.rounds) == 0 || e.Player == nil {
		return
	}
	c.rounds[len(c.rounds)-1].MVP = sid(e.Player)
}
