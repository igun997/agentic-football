package sim

import (
	"fmt"
	"math"
	"math/rand"
)

const (
	TickRate      = 30
	Dt            = 1.0 / TickRate
	FieldHalfX    = 50.0
	FieldHalfZ    = 30.0
	GoalHalfWidth = 7.0
	GoalDepth     = 4.0
	PlayerRadius  = 2.0
	BallRadius    = 1.0
	PostRadius    = 0.6

	PlayerMaxSpeed = 14.0
	BotSpeedFactor = 0.85 // bots are a notch slower than a well-driven agent
	PlayerAccel    = 55.0
	KickReach      = 1.6
	KickCooldown   = 0.4
	DashPower      = 26.0
	DashCooldown   = 2.0

	BlindAfter     = 5.0 // game seconds without an eye observation
	StaleTicks     = 90  // observation older than this makes commands cost double
	CountdownSecs  = 5.0
	GoalPauseSecs  = 2.0
	RestartWait    = 1.5
	SwitchCost     = 2.0
	SwitchCool     = 1.5
	CompilePerCost = 1.0 / 15.0
	AutopilotAfter = 8 * TickRate // ticks without a command before the bot AI covers for an idle agent
)

type Phase string

const (
	PhaseLobby     Phase = "lobby"
	PhaseCountdown Phase = "countdown"
	PhasePlaying   Phase = "playing"
	PhaseGoal      Phase = "goal"
	PhaseFinished  Phase = "finished"
)

type Role string

const (
	RoleGK  Role = "GK"
	RoleDEF Role = "DEF"
	RoleMID Role = "MID"
	RoleFWD Role = "FWD"
)

// GameError is a rule rejection that is reported back to the calling agent.
type GameError struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	RetryAfter float64 `json:"retry_after,omitempty"` // game seconds
}

func (e *GameError) Error() string { return e.Code + ": " + e.Message }

func gerr(code, format string, a ...any) *GameError {
	return &GameError{Code: code, Message: fmt.Sprintf(format, a...)}
}

type Event struct {
	Type string         `json:"type"`
	Tick int            `json:"tick"`
	Data map[string]any `json:"data,omitempty"`
}

// Player is a body on the pitch. It is driven by an Agent when one has taken it over,
// otherwise by the built-in bot AI (like an uncontrolled player in a football game).
type Player struct {
	ID   string
	Num  int
	Team int
	Role Role
	Home Vec // kickoff / formation slot in world coordinates

	Pos, Vel, Face, Move Vec
	Agent                *Agent

	KickCD, DashCD                 float64
	botRest                        float64 // bots give the ball room after playing it
	auto                           bool    // bot AI is currently driving this (idle agent or no agent)
	Frozen, Slowed, Hasted, Shield float64
}

// Agent is an AI coach: it owns the skills and controls one body at a time. Its speed is not
// capped by the game; throughput (TPS) is only measured, as a provider benchmark (see room.Bench).
type Agent struct {
	ID   string
	Name string
	Team int
	Body *Player

	Skill       [MaxSkillSlots]*Skill
	SkillCD     [MaxSkillSlots]float64
	CompileLeft float64
	RecoilLeft  float64
	SwitchCD    float64
	LastCmdTick int
	Brief       *Brief
	Ready       bool

	Strikes      int
	Message      string
	MessageTick  int
	Observations int
	Commands     int
}

type Ball struct {
	Pos Vec
	Vel Vec
}

type Projectile struct {
	ID      int
	Owner   string
	Team    int
	Pos     Vec
	Vel     Vec
	Radius  float64
	Life    float64
	OnHit   []Effect
	SkillID string
}

type Magnet struct {
	Owner    string
	Radius   float64
	Strength float64
	Left     float64
}

type Wall struct {
	ID   int
	A, B Vec
	Left float64
}

// Restart is a dead-ball situation: kickoff, throw-in, corner or goal kick.
type Restart struct {
	Kind    string
	Team    int // team that takes it
	Spot    Vec
	Wait    float64
	Keepout float64
}

type World struct {
	Tick      int
	Phase     Phase
	PhaseLeft float64
	TimeLeft  float64
	Duration  float64
	Score     [2]int
	Winner    int // -1 draw, 0/1 team, only when finished
	Players   []*Player
	Agents    []*Agent
	Ball      Ball
	Projs     []*Projectile
	Magnets   []*Magnet
	Walls     []*Wall
	Events    []Event
	Restart   *Restart

	PerTeam       int
	Autopilot     bool // bots cover for agents that stopped commanding
	LastTouchTeam int
	kickoffTeam   int
	nextID        int
	rng           *rand.Rand
}

// NewWorld creates the match with full squads of perTeam players each (bots until agents join).
func NewWorld(durationSecs float64, perTeam int) *World {
	w := &World{Phase: PhaseLobby, Duration: durationSecs, TimeLeft: durationSecs, Winner: -1,
		PerTeam: perTeam, Autopilot: true, LastTouchTeam: -1, rng: rand.New(rand.NewSource(7))}
	for team := 0; team < 2; team++ {
		for i, slot := range formation(perTeam) {
			home := slot.pos
			face := Vec{1, 0}
			if team == 1 {
				home.X, face = -home.X, Vec{-1, 0}
			}
			w.Players = append(w.Players, &Player{
				ID: fmt.Sprintf("t%dp%d", team, i+1), Num: i + 1, Team: team, Role: slot.role,
				Home: home, Pos: home, Face: face,
			})
		}
	}
	return w
}

type slot struct {
	role Role
	pos  Vec
}

// formation returns the home slots for team 0 (attacking +x). Index 0 is the goalkeeper.
func formation(n int) []slot {
	gk := slot{RoleGK, Vec{-46, 0}}
	switch {
	case n >= 11:
		return []slot{gk,
			{RoleDEF, Vec{-32, -21}}, {RoleDEF, Vec{-33, -7}}, {RoleDEF, Vec{-33, 7}}, {RoleDEF, Vec{-32, 21}},
			{RoleMID, Vec{-19, -21}}, {RoleMID, Vec{-20, -7}}, {RoleMID, Vec{-20, 7}}, {RoleMID, Vec{-19, 21}},
			{RoleFWD, Vec{-6, -8}}, {RoleFWD, Vec{-6, 8}}}
	case n >= 7:
		return []slot{gk, {RoleDEF, Vec{-31, -10}}, {RoleDEF, Vec{-31, 10}},
			{RoleMID, Vec{-18, -17}}, {RoleMID, Vec{-19, 0}}, {RoleMID, Vec{-18, 17}}, {RoleFWD, Vec{-6, 0}}}
	case n >= 5:
		return []slot{gk, {RoleDEF, Vec{-30, 0}}, {RoleMID, Vec{-17, -12}}, {RoleMID, Vec{-17, 12}}, {RoleFWD, Vec{-6, 0}}}
	}
	out := make([]slot, 0, n) // tiny squads (tests): forwards only
	for i := 0; i < n; i++ {
		out = append(out, slot{RoleFWD, Vec{-22, float64(i) * 8}})
	}
	return out
}

func (w *World) emit(t string, data map[string]any) {
	w.Events = append(w.Events, Event{Type: t, Tick: w.Tick, Data: data})
}

// DrainEvents returns and clears the pending events.
func (w *World) DrainEvents() []Event {
	e := w.Events
	w.Events = nil
	return e
}

// AddAgent seats an agent and hands it the most attacking free outfield player of its team.
func (w *World) AddAgent(id, name string, team int) *Agent {
	a := &Agent{ID: id, Name: name, Team: team}
	w.Agents = append(w.Agents, a)
	for _, role := range []Role{RoleFWD, RoleMID, RoleDEF, RoleGK} {
		for _, p := range w.Players {
			if p.Team == team && p.Role == role && p.Agent == nil {
				p.Agent, a.Body = a, p
				return a
			}
		}
	}
	return a
}

func (w *World) AgentByID(id string) *Agent {
	for _, a := range w.Agents {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (w *World) PlayerByID(id string) *Player {
	for _, p := range w.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (w *World) layout() {
	for _, p := range w.Players {
		p.Pos, p.Vel, p.Move = p.Home, Vec{}, Vec{}
		p.Face = Vec{1, 0}
		if p.Team == 1 {
			p.Face = Vec{-1, 0}
		}
	}
	w.Ball = Ball{}
}

// Start begins the kickoff countdown.
func (w *World) Start() {
	if w.Phase != PhaseLobby {
		return
	}
	w.layout()
	w.setPhase(PhaseCountdown, CountdownSecs)
}

func (w *World) setPhase(p Phase, left float64) {
	w.Phase, w.PhaseLeft = p, left
	w.emit("phase", map[string]any{"phase": string(p)})
}

func (w *World) setRestart(kind string, team int, spot Vec) {
	keep := 8.0
	if kind == "kickoff" {
		keep = 9.5
	}
	w.Restart = &Restart{Kind: kind, Team: team, Spot: spot, Wait: RestartWait, Keepout: keep}
	w.Ball.Pos, w.Ball.Vel = spot, Vec{}
	w.LastTouchTeam = -1
	w.emit("restart", map[string]any{"kind": kind, "team": team, "spot": spot})
}

// ---- commands ---------------------------------------------------------------

func (w *World) controllable(a *Agent) *GameError {
	if w.Phase != PhasePlaying {
		return gerr("not_playing", "match phase is %q", w.Phase)
	}
	if a.Body == nil {
		return gerr("no_body", "you control no player; call switch_player")
	}
	if a.Body.Frozen > 0 {
		return gerr("frozen", "your player is frozen for %.1fs", a.Body.Frozen)
	}
	return nil
}

func (w *World) SetMove(a *Agent, dir Vec) *GameError {
	if e := w.controllable(a); e != nil {
		return e
	}
	a.Commands++
	a.LastCmdTick = w.Tick
	l := dir.Len()
	if l > 1 {
		dir = dir.Scale(1 / l)
	}
	a.Body.Move = dir
	if l > 0.05 {
		a.Body.Face = dir.Norm()
	}
	return nil
}

func (w *World) touch(team int) { w.LastTouchTeam = team }

func (w *World) inReach(p *Player) bool {
	return w.Ball.Pos.Sub(p.Pos).Len() <= PlayerRadius+BallRadius+KickReach
}

func (w *World) strike(p *Player, d Vec, power float64) {
	w.Ball.Vel = d.Scale(12 + 38*power).Add(p.Vel.Scale(0.3))
	w.touch(p.Team)
	if w.Restart != nil && w.Restart.Team == p.Team {
		w.Restart = nil // set piece taken
	}
	w.emit("kick", map[string]any{"player": p.ID, "power": power})
}

func (w *World) Kick(a *Agent, dir Vec, power float64) (bool, *GameError) {
	if e := w.controllable(a); e != nil {
		return false, e
	}
	p := a.Body
	power = clamp(power, 0, 1)
	if p.KickCD > 0 {
		return false, &GameError{Code: "cooldown", Message: "kick cooling down", RetryAfter: round1(p.KickCD)}
	}
	a.Commands++
	a.LastCmdTick = w.Tick
	d := dir.Norm()
	if d == (Vec{}) {
		d = p.Face
	}
	p.Face = d
	p.KickCD = KickCooldown
	if !w.inReach(p) {
		return false, nil // swung and missed
	}
	if w.Restart != nil && w.Restart.Team != p.Team {
		return false, nil // opponents may not touch a set piece
	}
	w.strike(p, d, power)
	return true, nil
}

func (w *World) Dash(a *Agent, dir Vec) *GameError {
	if e := w.controllable(a); e != nil {
		return e
	}
	p := a.Body
	if p.DashCD > 0 {
		return &GameError{Code: "cooldown", Message: "dash cooling down", RetryAfter: round1(p.DashCD)}
	}
	a.Commands++
	a.LastCmdTick = w.Tick
	d := dir.Norm()
	if d == (Vec{}) {
		d = p.Face
	}
	p.Face = d
	p.Vel = p.Vel.Add(d.Scale(DashPower))
	p.DashCD = DashCooldown
	w.emit("dash", map[string]any{"player": p.ID})
	return nil
}

// SwitchPlayer hands the agent another teammate (like pressing the switch button in a football
// game). The player it leaves is taken over by the bot AI. target "" = the free teammate nearest the ball.
func (w *World) SwitchPlayer(a *Agent, target string) (*Player, *GameError) {
	if w.Phase == PhaseLobby || w.Phase == PhaseFinished {
		return nil, gerr("not_playing", "match phase is %q", w.Phase)
	}
	if a.SwitchCD > 0 {
		return nil, &GameError{Code: "cooldown", Message: "switch cooling down", RetryAfter: round1(a.SwitchCD)}
	}
	var np *Player
	if target == "" {
		best := math.MaxFloat64
		for _, p := range w.Players {
			if p.Team == a.Team && p.Agent == nil {
				if d := p.Pos.Sub(w.Ball.Pos).Len(); d < best {
					best, np = d, p
				}
			}
		}
		if np == nil {
			return nil, gerr("no_free_player", "no free teammate to switch to")
		}
	} else {
		np = w.PlayerByID(target)
		switch {
		case np == nil || np.Team != a.Team:
			return nil, gerr("bad_player", "player_id must be one of your own team's players (see observe.players)")
		case np.Agent == a:
			return nil, gerr("already_yours", "you already control %s", target)
		case np.Agent != nil:
			return nil, gerr("occupied", "%s is controlled by agent %s", target, np.Agent.Name)
		}
	}
	if a.Body != nil {
		a.Body.Agent, a.Body.Move = nil, Vec{}
	}
	np.Agent, a.Body = a, np
	a.SwitchCD = SwitchCool
	a.LastCmdTick = w.Tick
	w.emit("switch", map[string]any{"agent": a.ID, "player": np.ID, "role": string(np.Role), "num": np.Num})
	return np, nil
}

// CreateSkill is the live (in-match) way to author a skill. The consequence is the "compile lag": the
// author's player crawls for CreateCost/15 seconds and cannot cast; there is no TPS budget.
func (w *World) CreateSkill(a *Agent, slot int, spec SkillSpec) (*Skill, *GameError) {
	if slot < 0 || slot >= MaxSkillSlots {
		return nil, gerr("bad_slot", "slot must be 0..%d", MaxSkillSlots-1)
	}
	if w.Phase == PhaseFinished {
		return nil, gerr("not_playing", "match finished")
	}
	if w.Phase == PhaseLobby || w.Phase == PhaseCountdown {
		return nil, gerr("prematch", "before kickoff use forge_skill (free, follows your trainer's brief)")
	}
	if a.Brief != nil && a.Brief.LockLoadout {
		return nil, gerr("loadout_locked", "your trainer locked the loadout: no live forging during the match")
	}
	if a.CompileLeft > 0 {
		return nil, &GameError{Code: "compiling", Message: "still compiling a previous skill", RetryAfter: round1(a.CompileLeft)}
	}
	sk, err := CompileSkill(spec)
	if err != nil {
		return nil, gerr("invalid_skill", "%v", err)
	}
	if a.Brief != nil {
		if sb := a.Brief.SkillFor(slot); sb != nil {
			if err := sb.Check(spec, sk.Power); err != nil {
				return nil, gerr("guardrail", "%v", err)
			}
		}
	}
	w.nextID++
	sk.ID = fmt.Sprintf("sk%d", w.nextID)
	sk.Owner, sk.Slot = a.ID, slot
	a.Skill[slot] = sk
	a.SkillCD[slot] = 0
	a.CompileLeft = sk.CreateCost * CompilePerCost
	w.emit("skill_created", map[string]any{"player": a.ID, "skill": sk.ID, "name": sk.Spec.Name, "power": sk.Power, "cost": sk.CreateCost})
	return sk, nil
}

// ForgeSkill is the pre-match, trainer-guided way to build a skill: free of compile lag,
// but only allowed for slots the trainer briefed, and only if the result fits the brief.
func (w *World) ForgeSkill(a *Agent, slot int, spec SkillSpec) (*Skill, *GameError) {
	if w.Phase != PhaseLobby {
		return nil, gerr("not_prematch", "forge_skill only works before kickoff; in the match use create_skill (your player lags while it compiles)")
	}
	if slot < 0 || slot >= MaxSkillSlots {
		return nil, gerr("bad_slot", "slot must be 0..%d", MaxSkillSlots-1)
	}
	if a.Brief == nil || a.Brief.SkillFor(slot) == nil {
		return nil, gerr("no_brief", "your trainer did not brief slot %d (see get_brief)", slot)
	}
	sk, err := CompileSkill(spec)
	if err != nil {
		return nil, gerr("invalid_skill", "%v", err)
	}
	if err := a.Brief.SkillFor(slot).Check(spec, sk.Power); err != nil {
		return nil, gerr("guardrail", "%v", err)
	}
	w.nextID++
	sk.ID = fmt.Sprintf("sk%d", w.nextID)
	sk.Owner, sk.Slot = a.ID, slot
	a.Skill[slot] = sk
	w.emit("skill_forged", map[string]any{"player": a.ID, "skill": sk.ID, "name": sk.Spec.Name, "power": sk.Power, "slot": slot})
	return sk, nil
}

// ApplyBrief attaches the trainer's brief and, in the lobby, moves the agent onto its preferred position.
func (w *World) ApplyBrief(a *Agent, b *Brief) {
	a.Brief = b
	if w.Phase != PhaseLobby || b == nil || (a.Body != nil && a.Body.Role == b.Persona.PreferredRole) {
		return
	}
	for _, p := range w.Players {
		if p.Team == a.Team && p.Agent == nil && p.Role == b.Persona.PreferredRole {
			if a.Body != nil {
				a.Body.Agent = nil
			}
			p.Agent, a.Body = a, p
			return
		}
	}
}

func (w *World) UseSkill(a *Agent, slot int, aim Vec) *GameError {
	if e := w.controllable(a); e != nil {
		return e
	}
	if slot < 0 || slot >= MaxSkillSlots || a.Skill[slot] == nil {
		return gerr("bad_slot", "no skill in slot %d", slot)
	}
	if a.CompileLeft > 0 {
		return &GameError{Code: "compiling", Message: "skill compiler busy; your player is lagging", RetryAfter: round1(a.CompileLeft)}
	}
	sk := a.Skill[slot]
	if a.SkillCD[slot] > 0 {
		return &GameError{Code: "cooldown", Message: "skill cooling down", RetryAfter: round1(a.SkillCD[slot])}
	}
	a.Commands++
	a.LastCmdTick = w.Tick
	p := a.Body
	dir := aim.Sub(p.Pos).Norm()
	if dir == (Vec{}) {
		dir = p.Face
	}
	p.Face = dir
	a.SkillCD[slot] = sk.Cooldown
	a.RecoilLeft = sk.Recoil
	w.emit("skill_cast", map[string]any{
		"player": a.ID, "body": p.ID, "skill": sk.ID, "name": sk.Spec.Name,
		"origin": p.Pos, "aim": dir, "reach": sk.Reach, "duration_ms": sk.Spec.Visual.DurationMs,
	})
	for i := range sk.Spec.Effects {
		w.applyEffect(p, &sk.Spec.Effects[i], p.Pos, dir, sk.ID)
	}
	return nil
}

func (w *World) enemiesIn(p *Player, center Vec, radius float64) []*Player {
	var out []*Player
	for _, o := range w.Players {
		if o.Team != p.Team && o.Pos.Sub(center).Len() <= radius+PlayerRadius {
			out = append(out, o)
		}
	}
	return out
}

func (w *World) applyStatus(target *Player, caster *Player, status string, dur float64) {
	if target != caster && target.Shield > 0 && status != "shield" && status != "haste" {
		w.emit("shield_block", map[string]any{"player": target.ID, "from": caster.ID})
		return
	}
	switch status {
	case "freeze":
		target.Frozen = math.Max(target.Frozen, dur)
		target.Move = Vec{}
	case "slow":
		target.Slowed = math.Max(target.Slowed, dur)
	case "haste":
		target.Hasted = math.Max(target.Hasted, dur)
	case "shield":
		target.Shield = math.Max(target.Shield, dur)
	}
	w.emit("status", map[string]any{"player": target.ID, "status": status, "duration": dur})
}

func (w *World) blast(p *Player, e *Effect, center Vec) {
	for _, t := range e.Targets {
		switch t {
		case "ball":
			d := w.Ball.Pos.Sub(center)
			if d.Len() <= e.Radius+BallRadius {
				dir := d.Norm()
				if dir == (Vec{}) {
					dir = p.Face
				}
				w.Ball.Vel = w.Ball.Vel.Add(dir.Scale(e.Power))
				w.touch(p.Team)
			}
		case "enemies", "allies":
			for _, o := range w.Players {
				if o == p || (t == "enemies") == (o.Team == p.Team) {
					continue
				}
				d := o.Pos.Sub(center)
				if d.Len() <= e.Radius+PlayerRadius {
					if o.Team != p.Team && o.Shield > 0 {
						w.emit("shield_block", map[string]any{"player": o.ID, "from": p.ID})
						continue
					}
					o.Vel = o.Vel.Add(d.Norm().Scale(e.Power))
				}
			}
		}
	}
}

func (w *World) applyEffect(p *Player, e *Effect, origin, aim Vec, skillID string) {
	switch e.Type {
	case "dash_self":
		d := aim
		if e.Direction == "away" {
			d = aim.Scale(-1)
		}
		p.Vel = p.Vel.Add(d.Scale(e.Power))
	case "impulse_ball":
		if w.Ball.Pos.Sub(p.Pos).Len() <= e.Radius+PlayerRadius+BallRadius {
			d := aim
			switch e.Direction {
			case "toward":
				d = p.Pos.Sub(w.Ball.Pos).Norm()
			case "away":
				d = w.Ball.Pos.Sub(p.Pos).Norm()
			}
			w.Ball.Vel = d.Scale(e.Power)
			w.touch(p.Team)
		}
	case "blast":
		w.blast(p, e, origin)
	case "projectile":
		w.nextID++
		w.Projs = append(w.Projs, &Projectile{
			ID: w.nextID, Owner: p.ID, Team: p.Team,
			Pos: p.Pos.Add(aim.Scale(PlayerRadius + e.Radius + 0.2)), Vel: aim.Scale(e.Speed),
			Radius: e.Radius, Life: e.Lifetime, OnHit: e.OnHit, SkillID: skillID,
		})
	case "status":
		if e.Target == "self" {
			w.applyStatus(p, p, e.Status, e.Duration)
		} else {
			for _, o := range w.enemiesIn(p, origin, e.Radius) {
				w.applyStatus(o, p, e.Status, e.Duration)
			}
		}
	case "magnet":
		w.Magnets = append(w.Magnets, &Magnet{Owner: p.ID, Radius: e.Radius, Strength: e.Strength, Left: e.Duration})
	case "wall":
		c := p.Pos.Add(aim.Scale(e.Distance))
		perp := Vec{-aim.Z, aim.X}.Scale(e.Length / 2)
		w.nextID++
		w.Walls = append(w.Walls, &Wall{ID: w.nextID, A: c.Sub(perp), B: c.Add(perp), Left: e.Duration})
	}
}

func (w *World) Announce(a *Agent, text string) *GameError {
	if len(text) > 80 {
		text = text[:80]
	}
	if w.Tick-a.MessageTick < 3*TickRate && a.Message != "" {
		return gerr("rate_limited", "announce at most once per 3 game-seconds")
	}
	a.Message, a.MessageTick = text, w.Tick
	w.emit("announce", map[string]any{"player": a.ID, "text": text})
	return nil
}

// Strike records a rules violation by an agent: a yellow card plus a 3s sin-bin (slowed) for the
// player it drives, visible to spectators.
func (w *World) Strike(a *Agent, reason string) {
	a.Strikes++
	if a.Body != nil {
		a.Body.Slowed = math.Max(a.Body.Slowed, 3)
	}
	w.emit("strike", map[string]any{"player": a.ID, "reason": reason, "strikes": a.Strikes})
}
