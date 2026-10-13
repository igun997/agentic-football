// Package room owns match lifecycle: seats, role tokens, the tick loop and spectator fan-out.
package room

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"agentswars/internal/sim"
)

type Role string

const (
	RoleCommander Role = "commander"
	RoleEye       Role = "eye"
	RoleExecutor  Role = "executor"
)

type Config struct {
	PlayersPerTeam int     `json:"players_per_team"` // squad on the pitch per team: 5, 7 or 11
	AgentsPerTeam  int     `json:"agents_per_team"`  // AI agents (coaches) per team, the rest are bots
	DurationSecs   float64 `json:"duration_secs"`
	TimeScale      float64 `json:"time_scale"`
	PrepSecs       int     `json:"prep_secs"` // pre-match window for agents to forge skills
}

func (c *Config) Normalize() error {
	if c.PlayersPerTeam == 0 {
		c.PlayersPerTeam = 11
	}
	if c.AgentsPerTeam == 0 {
		c.AgentsPerTeam = 1
	}
	if c.DurationSecs == 0 {
		c.DurationSecs = 180
	}
	if c.TimeScale == 0 {
		c.TimeScale = 0.5
	}
	if c.PrepSecs == 0 {
		c.PrepSecs = 90
	}
	switch {
	case c.PlayersPerTeam != 5 && c.PlayersPerTeam != 7 && c.PlayersPerTeam != 11:
		return errors.New("players_per_team must be 5, 7 or 11")
	case c.AgentsPerTeam < 1 || c.AgentsPerTeam > 3:
		return errors.New("agents_per_team must be 1..3")
	case c.DurationSecs < 30 || c.DurationSecs > 600:
		return errors.New("duration_secs must be 30..600")
	case c.TimeScale < 0.25 || c.TimeScale > 1:
		return errors.New("time_scale must be 0.25..1")
	case c.PrepSecs < 10 || c.PrepSecs > 900:
		return errors.New("prep_secs must be 10..900")
	}
	return nil
}

type Seat struct {
	AgentID string          `json:"agent_id"`
	Name    string          `json:"name"`
	Team    int             `json:"team"`
	Slot    int             `json:"slot"`
	Tokens  map[Role]string `json:"-"`
}

type Room struct {
	Code    string
	Cfg     Config
	Created time.Time

	mu            sync.Mutex
	world         *sim.World
	seats         []*Seat
	subs          map[chan []byte]struct{}
	skillsVersion int
	feed          []sim.Event
	running       bool
	lastActive    time.Time
	HostToken     string
	slots         []*Slot
	bench         map[string]*Bench
	prepDeadline  time.Time
}

func newRoom(code string, cfg Config) *Room {
	r := &Room{
		Code: code, Cfg: cfg, Created: time.Now(), lastActive: time.Now(), HostToken: token(),
		world: sim.NewWorld(cfg.DurationSecs, cfg.PlayersPerTeam), subs: map[chan []byte]struct{}{}, bench: map[string]*Bench{},
	}
	for team := 0; team < 2; team++ {
		for i := 0; i < cfg.AgentsPerTeam; i++ {
			r.slots = append(r.slots, &Slot{Team: team, Index: i, Brief: sim.DefaultBrief()})
		}
	}
	return r
}

func token() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Join seats a new agent. It issues one token per role so the MCP layer can enforce the
// commander / eye / executor split.
// Slot is a seat the human trainer sets up before any agent has joined (Pokémon style).
type Slot struct {
	Team   int       `json:"team"`
	Index  int       `json:"index"`
	Brief  sim.Brief `json:"brief"`
	Agent  string    `json:"agent_id,omitempty"`
	Locked bool      `json:"-"`
}

// Join seats a new agent in the first free slot of the emptier team and hands it the trainer's brief.
// It issues one token per role so the MCP layer can enforce the commander / eye / executor split.
func (r *Room) Join(name, provider, model string) (*Seat, *sim.Brief, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.world.Phase != sim.PhaseLobby {
		return nil, nil, errors.New("match already started; room is closed to new agents")
	}
	if len(r.seats) >= 2*r.Cfg.AgentsPerTeam {
		return nil, nil, errors.New("room is full")
	}
	if len(name) == 0 || len(name) > 24 {
		return nil, nil, errors.New("name must be 1..24 chars")
	}
	var cnt [2]int
	for _, s := range r.seats {
		cnt[s.Team]++
	}
	team := 0
	if cnt[1] < cnt[0] {
		team = 1
	}
	var slot *Slot
	for _, sl := range r.slots {
		if sl.Team == team && sl.Agent == "" {
			slot = sl
			break
		}
	}
	id := fmt.Sprintf("a%d", len(r.seats)+1)
	slot.Agent = id
	seat := &Seat{AgentID: id, Name: name, Team: team, Slot: slot.Index, Tokens: map[Role]string{
		RoleCommander: token(), RoleEye: token(), RoleExecutor: token(),
	}}
	r.seats = append(r.seats, seat)
	ag := r.world.AddAgent(id, name, team)
	brief := slot.Brief
	r.world.ApplyBrief(ag, &brief)
	bn := newBench()
	bn.Provider, bn.Model = cleanLabel(provider), cleanLabel(model)
	r.bench[id] = bn
	r.world.Events = append(r.world.Events, sim.Event{Type: "join", Tick: r.world.Tick, Data: map[string]any{"player": id, "name": name, "team": team, "body": ag.Body.ID, "style": brief.Persona.Style}})
	r.broadcastLocked()
	if len(r.seats) == 2*r.Cfg.AgentsPerTeam {
		r.maybeStartLocked()
		if r.world.Phase == sim.PhaseLobby { // not everyone is ready: start anyway after the prep window
			window := time.Duration(r.Cfg.PrepSecs) * time.Second
			r.prepDeadline = time.Now().Add(window)
			time.AfterFunc(window, func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				if r.world.Phase == sim.PhaseLobby {
					r.startLocked()
				}
			})
		}
	}
	return seat, &brief, nil
}

func cleanLabel(s string) string {
	out := make([]rune, 0, 32)
	for _, c := range s {
		if c >= 32 && c < 127 && c != '<' && c != '>' && len(out) < 32 {
			out = append(out, c)
		}
	}
	return string(out)
}

func (r *Room) maybeStartLocked() {
	if len(r.seats) < 2*r.Cfg.AgentsPerTeam {
		return
	}
	for _, a := range r.world.Agents {
		if !a.Ready {
			return
		}
	}
	r.startLocked()
}

// Ready locks an agent's loadout. When every seat is filled and ready the match starts.
func (r *Room) Ready(agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.world.AgentByID(agentID)
	if a == nil {
		return errors.New("unknown agent")
	}
	if r.world.Phase != sim.PhaseLobby {
		return errors.New("match already started")
	}
	a.Ready = true
	r.world.Events = append(r.world.Events, sim.Event{Type: "ready", Tick: r.world.Tick, Data: map[string]any{"player": agentID}})
	r.broadcastLocked()
	r.maybeStartLocked()
	return nil
}

// SetBrief lets the host edit a slot's brief while the room is still in the lobby.
func (r *Room) SetBrief(team, index int, b sim.Brief) error {
	if err := b.Normalize(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.world.Phase != sim.PhaseLobby {
		return errors.New("briefs can only be edited before kickoff")
	}
	for _, sl := range r.slots {
		if sl.Team == team && sl.Index == index {
			sl.Brief = b
			if sl.Agent != "" {
				if a := r.world.AgentByID(sl.Agent); a != nil {
					nb := b
					r.world.ApplyBrief(a, &nb)
					// forged skills that no longer fit the new brief are withdrawn
					for i, sk := range a.Skill {
						if sk == nil {
							continue
						}
						sb := nb.SkillFor(i)
						if sb == nil || sb.Check(sk.Spec, sk.Power) != nil {
							a.Skill[i] = nil
						}
					}
					a.Ready = false
					r.world.Events = append(r.world.Events, sim.Event{Type: "brief_updated", Tick: r.world.Tick, Data: map[string]any{"player": sl.Agent}})
					r.skillsVersion++
				}
			}
			r.broadcastLocked()
			return nil
		}
	}
	return errors.New("no such slot")
}

// RecordCall is the benchmark hook used by the MCP layer for every tool call.
func (r *Room) RecordCall(agentID string, role Role, tool string, start, end time.Time, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b := r.bench[agentID]; b != nil {
		b.record(role, tool, start, end, failed)
	}
}

// ReportUsage stores self-reported token usage: output tokens and how long the generation took.
func (r *Room) ReportUsage(agentID string, in, out int, genMs float64, provider, model string) error {
	if in < 0 || out < 0 || out > 10_000_000 || genMs < 0 || genMs > 3_600_000 {
		return errors.New("usage values out of range")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.bench[agentID]
	if b == nil {
		return errors.New("unknown agent")
	}
	b.TokensIn += in
	b.TokensOut += out
	b.GenMs += genMs
	b.Reports++
	if p := cleanLabel(provider); p != "" {
		b.Provider = p
	}
	if m := cleanLabel(model); m != "" {
		b.Model = m
	}
	return nil
}

// Bench returns the benchmark of every agent, best first.
func (r *Room) Bench() []BenchEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.benchLocked()
}

type BenchEntry struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Team    int    `json:"team"`
	BenchView
}

func (r *Room) benchLocked() []BenchEntry {
	now := time.Now()
	out := []BenchEntry{}
	for _, a := range r.world.Agents {
		if b := r.bench[a.ID]; b != nil {
			out = append(out, BenchEntry{AgentID: a.ID, Name: a.Name, Team: a.Team, BenchView: b.view(now)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TokensPerSec != out[j].TokensPerSec {
			return out[i].TokensPerSec > out[j].TokensPerSec
		}
		return out[i].CallsAvg > out[j].CallsAvg
	})
	return out
}

func (r *Room) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seats) == 0 {
		return errors.New("need at least one agent in the room (empty seats are played by bots)")
	}
	if r.world.Phase != sim.PhaseLobby {
		return errors.New("match already started")
	}
	r.startLocked()
	return nil
}

func (r *Room) startLocked() {
	r.world.Start()
	if !r.running {
		r.running = true
		go r.loop()
	}
}

func (r *Room) loop() {
	interval := time.Duration(float64(time.Second) / (sim.TickRate * r.Cfg.TimeScale))
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		r.mu.Lock()
		r.world.Step()
		r.broadcastLocked()
		done := r.world.Phase == sim.PhaseFinished
		r.mu.Unlock()
		if done {
			return
		}
	}
}

// With runs fn on the world under the room lock.
func (r *Room) With(fn func(w *sim.World)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastActive = time.Now()
	fn(r.world)
	// events caused by commands are published right away so spectators never wait for the next tick
	r.broadcastLocked()
}

func (r *Room) Seat(agentID string) *Seat {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seats {
		if s.AgentID == agentID {
			return s
		}
	}
	return nil
}

// ---- views ----------------------------------------------------------------

type SkillView struct {
	Slot     int     `json:"slot"`
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Power    float64 `json:"power"`
	Cooldown float64 `json:"cooldown"`
	CDLeft   float64 `json:"cd_left"`
	CastCost float64 `json:"cast_cost"`
}

// PlayerView is a body on the pitch (all 22 are always visible).
type PlayerView struct {
	ID     string             `json:"id"`
	Num    int                `json:"num"`
	Team   int                `json:"team"`
	Role   sim.Role           `json:"role"`
	Pos    sim.Vec            `json:"pos"`
	Vel    sim.Vec            `json:"vel"`
	Face   sim.Vec            `json:"face"`
	Status map[string]float64 `json:"status,omitempty"`
	Agent  string             `json:"agent,omitempty"` // controlling agent id; empty = bot
}

type AgentView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Team        int         `json:"team"`
	Controls    string      `json:"controls"`
	Ready       bool        `json:"ready"`
	Style       string      `json:"style,omitempty"`
	Motto       string      `json:"motto,omitempty"`
	Bench       *BenchView  `json:"bench,omitempty"`
	Strikes     int         `json:"strikes"`
	Message     string      `json:"message,omitempty"`
	CompileLeft float64     `json:"compile_left,omitempty"`
	RecoilLeft  float64     `json:"recoil_left,omitempty"`
	SwitchCD    float64     `json:"switch_cd,omitempty"`
	Skills      []SkillView `json:"skills,omitempty"`
}

type ProjView struct {
	ID     int     `json:"id"`
	Pos    sim.Vec `json:"pos"`
	Radius float64 `json:"radius"`
	Team   int     `json:"team"`
	Skill  string  `json:"skill"`
}

type WallView struct {
	ID   int     `json:"id"`
	A    sim.Vec `json:"a"`
	B    sim.Vec `json:"b"`
	Left float64 `json:"left"`
}

type RestartView struct {
	Kind string  `json:"kind"`
	Team int     `json:"team"`
	Spot sim.Vec `json:"spot"`
}

type Snapshot struct {
	Room          string       `json:"room"`
	Tick          int          `json:"tick"`
	Phase         sim.Phase    `json:"phase"`
	PhaseLeft     float64      `json:"phase_left"`
	TimeLeft      float64      `json:"time_left"`
	Score         [2]int       `json:"score"`
	Winner        int          `json:"winner"`
	BallPos       sim.Vec      `json:"ball_pos"`
	BallVel       sim.Vec      `json:"ball_vel"`
	BallH         float64      `json:"ball_h"` // height of the ball centre above the ground (1 = rolling)
	Restart       *RestartView `json:"restart,omitempty"`
	Players       []PlayerView `json:"players"`
	Agents        []AgentView  `json:"agents"`
	Projectiles   []ProjView   `json:"projectiles"`
	Walls         []WallView   `json:"walls"`
	Events        []sim.Event  `json:"events,omitempty"`
	SkillsVersion int          `json:"skills_version"`
	You           string       `json:"you,omitempty"`         // your agent id (observe only)
	YourPlayer    string       `json:"your_player,omitempty"` // the body you control now
	AttackDir     string       `json:"attack_direction,omitempty"`
}

func (r *Room) snapshotLocked(w *sim.World, viewer string, withEvents bool) Snapshot {
	s := Snapshot{
		Room: r.Code, Tick: w.Tick, Phase: w.Phase, PhaseLeft: w.PhaseLeft, TimeLeft: w.TimeLeft,
		Score: w.Score, Winner: w.Winner, BallPos: w.Ball.Pos, BallVel: w.Ball.Vel, BallH: sim.BallRadius + w.Ball.Y,
		SkillsVersion: r.skillsVersion, You: viewer,
		Players: []PlayerView{}, Agents: []AgentView{}, Projectiles: []ProjView{}, Walls: []WallView{},
	}
	if rs := w.Restart; rs != nil {
		s.Restart = &RestartView{Kind: rs.Kind, Team: rs.Team, Spot: rs.Spot}
	}
	for _, p := range w.Players {
		pv := PlayerView{ID: p.ID, Num: p.Num, Team: p.Team, Role: p.Role, Pos: p.Pos, Vel: p.Vel, Face: p.Face}
		if p.Agent != nil {
			pv.Agent = p.Agent.ID
		}
		st := map[string]float64{}
		for k, v := range map[string]float64{"frozen": p.Frozen, "slowed": p.Slowed, "hasted": p.Hasted, "shield": p.Shield} {
			if v > 0 {
				st[k] = v
			}
		}
		if len(st) > 0 {
			pv.Status = st
		}
		s.Players = append(s.Players, pv)
	}
	for _, a := range w.Agents {
		av := AgentView{ID: a.ID, Name: a.Name, Team: a.Team, Strikes: a.Strikes, Message: a.Message,
			CompileLeft: a.CompileLeft, RecoilLeft: a.RecoilLeft, SwitchCD: a.SwitchCD}
		if a.Body != nil {
			av.Controls = a.Body.ID
		}
		if viewer == a.ID {
			s.YourPlayer = av.Controls
			s.AttackDir = "+x (attack the goal at x=+50, defend x=-50)"
			if a.Team == 1 {
				s.AttackDir = "-x (attack the goal at x=-50, defend x=+50)"
			}
		}
		av.Ready = a.Ready
		if a.Brief != nil {
			av.Style, av.Motto = a.Brief.Persona.Style, a.Brief.Persona.Motto
		}
		if bn := r.bench[a.ID]; bn != nil && (viewer == "" || viewer == a.ID) {
			v := bn.view(time.Now())
			av.Bench = &v
		}
		for i, sk := range a.Skill {
			if sk != nil {
				av.Skills = append(av.Skills, SkillView{Slot: i, ID: sk.ID, Name: sk.Spec.Name, Power: sk.Power,
					Cooldown: sk.Cooldown, CDLeft: a.SkillCD[i], CastCost: sk.CastCost})
			}
		}
		s.Agents = append(s.Agents, av)
	}
	for _, p := range w.Projs {
		s.Projectiles = append(s.Projectiles, ProjView{ID: p.ID, Pos: p.Pos, Radius: p.Radius, Team: p.Team, Skill: p.SkillID})
	}
	for _, wl := range w.Walls {
		s.Walls = append(s.Walls, WallView{ID: wl.ID, A: wl.A, B: wl.B, Left: wl.Left})
	}
	return s
}

// Observe is the eye subagent's view (own TPS only, no event stream).
func (r *Room) Observe(agentID string) Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(r.world, agentID, false)
}

func (r *Room) broadcastLocked() {
	events := r.world.DrainEvents()
	for _, e := range events {
		r.logEvent(e)
		if e.Type == "skill_created" || e.Type == "skill_forged" {
			r.skillsVersion++
		}
		if e.Type != "dash" && e.Type != "kick" && e.Type != "restart_taken" {
			r.feed = append(r.feed, e)
		}
	}
	if len(r.feed) > 60 {
		r.feed = r.feed[len(r.feed)-60:]
	}
	if len(r.subs) == 0 {
		return
	}
	snap := r.snapshotLocked(r.world, "", true)
	snap.Events = events
	b, _ := json.Marshal(snap)
	for ch := range r.subs {
		select {
		case ch <- b:
		default: // slow spectator: drop the frame rather than stall the sim
		}
	}
}

func (r *Room) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 8)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	snap := r.snapshotLocked(r.world, "", true)
	r.mu.Unlock()
	b, _ := json.Marshal(snap)
	ch <- b
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

type SlotView struct {
	Team  int       `json:"team"`
	Index int       `json:"index"`
	Brief sim.Brief `json:"brief"`
	Agent string    `json:"agent_id,omitempty"`
	Name  string    `json:"name,omitempty"`
	Ready bool      `json:"ready"`
}

type Info struct {
	Code           string       `json:"code"`
	Config         Config       `json:"config"`
	Phase          sim.Phase    `json:"phase"`
	Seats          []*Seat      `json:"seats"`
	Slots          []SlotView   `json:"slots"`
	Skills         []*sim.Skill `json:"skills"`
	Feed           []sim.Event  `json:"feed"`
	Snapshot       Snapshot     `json:"snapshot"`
	SeatsFree      int          `json:"seats_free"`
	PrepDeadlineMs int64        `json:"prep_deadline_ms,omitempty"`
	Bench          []BenchEntry `json:"bench"`
}

func (r *Room) Info() Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := Info{Code: r.Code, Config: r.Cfg, Phase: r.world.Phase, Seats: append([]*Seat{}, r.seats...),
		Feed: append([]sim.Event{}, r.feed...), Snapshot: r.snapshotLocked(r.world, "", false),
		SeatsFree: 2*r.Cfg.AgentsPerTeam - len(r.seats), Skills: []*sim.Skill{}, Bench: r.benchLocked(), Slots: []SlotView{}}
	if !r.prepDeadline.IsZero() && r.world.Phase == sim.PhaseLobby {
		info.PrepDeadlineMs = r.prepDeadline.UnixMilli()
	}
	for _, sl := range r.slots {
		sv := SlotView{Team: sl.Team, Index: sl.Index, Brief: sl.Brief, Agent: sl.Agent}
		if a := r.world.AgentByID(sl.Agent); a != nil {
			sv.Name, sv.Ready = a.Name, a.Ready
		}
		info.Slots = append(info.Slots, sv)
	}
	for _, p := range r.world.Agents {
		for _, sk := range p.Skill {
			if sk != nil {
				info.Skills = append(info.Skills, sk)
			}
		}
	}
	return info
}

// CheckHost reports whether tok is this room's host token.
func (r *Room) CheckHost(tok string) bool {
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(r.HostToken)) == 1
}

func (r *Room) idleFor() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.lastActive)
}

// logEvent writes the notable match events to the server log (one line each).
func (r *Room) logEvent(e sim.Event) {
	switch e.Type {
	case "join", "ready", "brief_updated", "skill_forged", "skill_created", "skill_cast", "skill_result", "projectile_hit",
		"status", "impact", "switch", "goal", "restart", "strike", "final", "shield_block":
		d, _ := json.Marshal(e.Data)
		log.Printf("[%s t=%d] %s %s", r.Code, e.Tick, e.Type, d)
	}
}
