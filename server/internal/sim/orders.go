package sim

import (
	"fmt"
	"math"
)

// Precision command layer. An agent drives one disc, but it can also command every other disc of
// its team: set_plan gives movement orders with exact arrival times, tactics reshape the whole
// team, pass_to computes the exact power/loft/lead for a pass (and sends the receiver to the spot
// at the right moment), launch flicks any disc to an exact stopping point like a carrom piece.

const (
	MaxOrders     = 11
	OrderMaxTTL   = 12.0
	OrderDefTTL   = 5.0
	LaunchCool    = 3.0
	maxLaunchDist = 40.0 * 40.0 / (2 * PlayerAccel) // distance a full-speed flick coasts (≈14.5)
)

type Order struct {
	Action   string // move | hold | mark | support | press | coast
	Target   Vec
	TargetID string
	Offset   Vec
	Speed    float64 // 0..1 speed cap (arrive_s turns into an exact cap)
	Until    int     // tick the order expires
	ETA      float64 // seconds it takes to arrive, as promised to the agent
	Next     *Order  // follow-up when this one ends (launch: coast, then hold)
	AgentID  string
}

type Tactics struct {
	Line  string `json:"line"`  // high | mid | low
	Width string `json:"width"` // wide | normal | narrow
	Press bool   `json:"press"` // always send a second player at the ball
}

type OrderSpec struct {
	PlayerID string  `json:"player_id"`
	Action   string  `json:"action"`
	X        float64 `json:"x"`
	Z        float64 `json:"z"`
	TargetID string  `json:"target_id"`
	TTL      float64 `json:"ttl"`      // game seconds the order lasts (default 5, max 12)
	ArriveS  float64 `json:"arrive_s"` // move only: arrive exactly this many seconds from now
}

type OrderResult struct {
	PlayerID string  `json:"player_id"`
	Action   string  `json:"action"`
	Distance float64 `json:"distance,omitempty"`
	ETA      float64 `json:"eta_s,omitempty"`
	Feasible bool    `json:"feasible"`
	Note     string  `json:"note,omitempty"`
}

func (t *Tactics) normalize() error {
	if t.Line == "" {
		t.Line = "mid"
	}
	if t.Width == "" {
		t.Width = "normal"
	}
	if !oneIn(t.Line, "high", "mid", "low") {
		return fmt.Errorf("tactics.line must be high|mid|low")
	}
	if !oneIn(t.Width, "wide", "normal", "narrow") {
		return fmt.Errorf("tactics.width must be wide|normal|narrow")
	}
	return nil
}

func (w *World) clampPitch(v Vec) Vec {
	return Vec{clamp(v.X, -FieldHalfX+PlayerRadius, FieldHalfX-PlayerRadius), clamp(v.Z, -FieldHalfZ+PlayerRadius, FieldHalfZ-PlayerRadius)}
}

// botMaxSpeed is the top speed of a bot-driven disc (what arrive_s is computed against).
func botMaxSpeed() float64 { return PlayerMaxSpeed * BotSpeedFactor }

// SetPlan replaces (or extends) the team's movement orders and tactics. Every order gets an exact ETA back.
func (w *World) SetPlan(a *Agent, specs []OrderSpec, tac *Tactics, replace bool) ([]OrderResult, *GameError) {
	if w.Phase == PhaseLobby || w.Phase == PhaseFinished {
		return nil, gerr("not_playing", "match phase is %q", w.Phase)
	}
	if len(specs) > MaxOrders {
		return nil, gerr("too_many_orders", "at most %d orders per plan", MaxOrders)
	}
	if tac != nil {
		if err := tac.normalize(); err != nil {
			return nil, gerr("bad_tactics", "%v", err)
		}
	}
	type staged struct {
		p *Player
		o *Order
		r OrderResult
	}
	var plan []staged
	seen := map[string]bool{}
	own := Vec{-attackDir(a.Team) * FieldHalfX, 0}
	for i, sp := range specs {
		p := w.PlayerByID(sp.PlayerID)
		switch {
		case p == nil || p.Team != a.Team:
			return nil, gerr("bad_player", "orders[%d]: %q is not a player of your team", i, sp.PlayerID)
		case p == a.Body:
			return nil, gerr("you_drive_him", "orders[%d]: you drive %s yourself (use move/kick/launch)", i, p.ID)
		case p.Agent != nil && !p.auto:
			return nil, gerr("other_agent", "orders[%d]: %s is driven by agent %s", i, p.ID, p.Agent.Name)
		case seen[p.ID]:
			return nil, gerr("duplicate_order", "orders[%d]: two orders for %s", i, p.ID)
		}
		seen[p.ID] = true
		ttl := sp.TTL
		if ttl == 0 {
			ttl = OrderDefTTL
		}
		if ttl < 1 || ttl > OrderMaxTTL {
			return nil, gerr("bad_ttl", "orders[%d]: ttl must be 1..%.0f game-seconds", i, OrderMaxTTL)
		}
		o := &Order{Action: sp.Action, Until: w.Tick + int(ttl*TickRate), Speed: 1, AgentID: a.ID}
		r := OrderResult{PlayerID: p.ID, Action: sp.Action, Feasible: true}
		switch sp.Action {
		case "move":
			o.Target = w.clampPitch(Vec{sp.X, sp.Z})
			d := o.Target.Sub(p.Pos).Len()
			r.Distance = round1(d)
			vmax := botMaxSpeed()
			o.ETA = d / vmax
			if sp.ArriveS > 0 {
				if sp.ArriveS < 0.5 || sp.ArriveS > OrderMaxTTL {
					return nil, gerr("bad_arrive", "orders[%d]: arrive_s must be 0.5..%.0f", i, OrderMaxTTL)
				}
				need := d / (sp.ArriveS * vmax)
				if need > 1 {
					r.Feasible, r.Note = false, fmt.Sprintf("cannot arrive in %.1fs: fastest is %.1fs (running flat out)", sp.ArriveS, d/vmax)
				} else {
					o.Speed = math.Max(0.15, need)
					o.ETA = sp.ArriveS
				}
			}
			if o.ETA+0.1 > ttl {
				o.Until = w.Tick + int((o.ETA+1.5)*TickRate) // keep him there a moment after he arrives
			}
			r.ETA = round1(o.ETA)
		case "hold":
			o.Target = p.Pos
		case "mark":
			opp := w.PlayerByID(sp.TargetID)
			if opp == nil || opp.Team == a.Team {
				return nil, gerr("bad_target", "orders[%d]: mark needs target_id of an opposing player", i)
			}
			o.TargetID = opp.ID
			_ = own
		case "support":
			mate := w.PlayerByID(sp.TargetID)
			if mate == nil || mate.Team != a.Team || mate == p {
				return nil, gerr("bad_target", "orders[%d]: support needs target_id of another player of your team (x,z = offset from him)", i)
			}
			o.TargetID, o.Offset = mate.ID, Vec{clamp(sp.X, -20, 20), clamp(sp.Z, -20, 20)}
		case "press":
		default:
			return nil, gerr("bad_action", "orders[%d]: action must be move|hold|mark|support|press", i)
		}
		plan = append(plan, staged{p, o, r})
	}
	if replace {
		for _, p := range w.Players {
			if p.Team == a.Team && p.order != nil && p.order.AgentID == a.ID {
				p.order = nil
			}
		}
	}
	var results []OrderResult
	for _, s := range plan {
		s.p.order = s.o
		results = append(results, s.r)
	}
	if tac != nil {
		w.Tactics[a.Team] = *tac
	}
	a.LastCmdTick = w.Tick
	w.emit("plan_set", map[string]any{"agent": a.ID, "orders": len(plan), "tactics": w.Tactics[a.Team]})
	return results, nil
}

// ClearPlan drops all orders of the agent's team (the bots return to their normal AI).
func (w *World) ClearPlan(a *Agent) int {
	n := 0
	for _, p := range w.Players {
		if p.Team == a.Team && p.order != nil {
			p.order, n = nil, n+1
		}
	}
	w.Tactics[a.Team] = Tactics{}
	return n
}

// runOrder drives an ordered bot. Returns false when the order is over.
func (w *World) runOrder(p *Player) bool {
	o := p.order
	if o == nil {
		return false
	}
	if w.Tick >= o.Until {
		p.order = o.Next
		return p.order != nil && w.runOrder(p)
	}
	own := Vec{-attackDir(p.Team) * FieldHalfX, 0}
	switch o.Action {
	case "coast": // a launched disc slides to its stop
		p.Move = Vec{}
	case "hold":
		p.Move = Vec{}
	case "press":
		w.thinkPresser(p)
	case "mark":
		opp := w.PlayerByID(o.TargetID)
		if opp == nil {
			return false
		}
		w.moveCapped(p, opp.Pos.Add(own.Sub(opp.Pos).Norm().Scale(3.6)), o.Speed)
	case "support":
		mate := w.PlayerByID(o.TargetID)
		if mate == nil {
			return false
		}
		w.moveCapped(p, w.clampPitch(mate.Pos.Add(o.Offset)), o.Speed)
	default: // move
		w.moveCapped(p, o.Target, o.Speed)
	}
	return true
}

// moveCapped is moveTo with a speed cap: it arrives at the cap speed instead of sprinting, which is
// what makes arrive_s exact.
func (w *World) moveCapped(p *Player, target Vec, cap float64) {
	d := target.Sub(p.Pos)
	l := d.Len()
	if l < 0.6 {
		p.Move = Vec{}
		return
	}
	p.Move = d.Scale(1 / l).Scale(math.Min(cap, math.Max(0.1, l/3)))
	p.Face = d.Norm()
}

// ---- launch (carrom flick) ------------------------------------------------

type LaunchResult struct {
	From, Stop Vec
	Speed, ETA float64
	Clamped    bool
}

// Launch flicks one of the agent's discs towards a point with exactly the speed that makes it
// coast to a stop there (deceleration is PlayerAccel): v = sqrt(2·a·d). Max flick is ~14.5 units.
func (w *World) Launch(a *Agent, playerID string, target Vec) (*LaunchResult, *GameError) {
	if w.Phase != PhasePlaying {
		return nil, gerr("not_playing", "match phase is %q", w.Phase)
	}
	p := w.PlayerByID(playerID)
	switch {
	case p == nil || p.Team != a.Team:
		return nil, gerr("bad_player", "%q is not a player of your team", playerID)
	case p.Agent != nil && p.Agent != a && !p.auto:
		return nil, gerr("other_agent", "%s is driven by agent %s", p.ID, p.Agent.Name)
	case p.Frozen > 0:
		return nil, gerr("frozen", "%s is frozen for %.1fs", p.ID, p.Frozen)
	case p.launchCD > 0:
		return nil, &GameError{Code: "cooldown", Message: "that disc was flicked a moment ago", RetryAfter: round1(p.launchCD)}
	}
	target = w.clampPitch(target)
	d := target.Sub(p.Pos)
	dist := d.Len()
	if dist < 1 {
		return nil, gerr("too_close", "target is closer than 1 unit")
	}
	res := &LaunchResult{From: p.Pos}
	if dist > maxLaunchDist {
		dist, res.Clamped = maxLaunchDist, true
	}
	dir := d.Norm()
	v := math.Sqrt(2 * PlayerAccel * dist)
	res.Speed, res.ETA, res.Stop = v, v/PlayerAccel, p.Pos.Add(dir.Scale(dist))
	p.Vel, p.Move, p.Face = dir.Scale(v), Vec{}, dir
	p.launchCD = LaunchCool
	if p.Agent == nil || p.auto { // bots coast to the stop, then hold the spot for a moment
		p.order = &Order{Action: "coast", Until: w.Tick + int((res.ETA+0.15)*TickRate), AgentID: a.ID,
			Next: &Order{Action: "move", Target: res.Stop, Speed: 0.4, Until: w.Tick + int((res.ETA+3)*TickRate), AgentID: a.ID}}
	}
	a.LastCmdTick = w.Tick
	w.emit("launch", map[string]any{"agent": a.ID, "player": p.ID, "from": res.From, "to": res.Stop, "speed": round1(v), "eta": round1(res.ETA)})
	return res, nil
}

// ---- precise passing ----------------------------------------------------------

type PassResult struct {
	Dir     Vec     `json:"dir"`
	Power   float64 `json:"power"`
	Lift    float64 `json:"lift"`
	Style   string  `json:"style"`
	Aim     Vec     `json:"aim"`
	Dist    float64 `json:"distance"`
	ETA     float64 `json:"eta_s"`
	Receive string  `json:"receiver_order,omitempty"`
	Risk    string  `json:"risk,omitempty"`
}

// groundPass: power so that the rolling ball is down to a receivable 6 u/s exactly at the target.
func groundPass(dist float64) (power, eta float64) {
	v0 := 6 + dist*0.75
	power = (v0 - 12) / 38
	eta = math.Log(6/v0) / math.Log(0.975) / TickRate
	return
}

// lobPass: horizontal speed so that the ball lands at the target after one flight (gravity + air drag).
func lobPass(dist, lift float64) (power, eta float64) {
	vy := lift * 26
	flight := 2 * vy / Gravity
	n := flight * TickRate
	vh := dist * (1 - 0.996) / (Dt * (1 - math.Pow(0.996, n)))
	power = (vh/(1-0.3*lift) - 12) / 38
	return power, flight
}

// PassTo kicks the ball so that it arrives at a team-mate (leading him) or at a point, with exact
// power/loft, and optionally orders a bot receiver to be at the spot when the ball arrives.
func (w *World) PassTo(a *Agent, targetID string, point *Vec, style string, lift float64, receive bool) (*PassResult, *GameError) {
	if e := w.controllable(a); e != nil {
		return nil, e
	}
	p := a.Body
	if p.KickCD > 0 {
		return nil, &GameError{Code: "cooldown", Message: "kick cooling down", RetryAfter: round1(p.KickCD)}
	}
	if !w.inReach(p) {
		return nil, gerr("no_ball", "the ball is not at your feet (distance %.1f, reach %.1f, height %.1f)", w.Ball.Pos.Sub(p.Pos).Len(), PlayerRadius+BallRadius+KickReach, w.Ball.Y)
	}
	if w.Restart != nil && w.Restart.Team != p.Team {
		return nil, gerr("set_piece", "the opponents' set piece")
	}
	if style == "" {
		style = "auto"
	}
	if !oneIn(style, "auto", "ground", "lob") {
		return nil, gerr("bad_style", "style must be auto|ground|lob")
	}
	var mate *Player
	aim := Vec{}
	if targetID != "" {
		mate = w.PlayerByID(targetID)
		if mate == nil || mate.Team != p.Team || mate == p {
			return nil, gerr("bad_player", "player_id must be another player of your team")
		}
		aim = mate.Pos
	} else if point != nil {
		aim = w.clampPitch(*point)
	} else {
		return nil, gerr("no_target", "give player_id or x,z")
	}
	ball := w.Ball.Pos
	compute := func(at Vec, st string, lf float64) (pw, eta float64) {
		d := at.Sub(ball).Len()
		if st == "lob" {
			return lobPass(d, lf)
		}
		return groundPass(d)
	}
	if lift <= 0 {
		lift = 0.7
	}
	lift = clamp(lift, 0.3, 1)
	chosen := style
	if chosen == "auto" {
		chosen = "ground"
		if d := aim.Sub(ball).Len(); d > 34 || w.blocked(p.Team, ball, aim.Sub(ball).Norm(), d) {
			chosen = "lob"
		}
	}
	// lead the receiver: where will he be when the ball arrives? (2 refinement passes)
	if mate != nil {
		for i := 0; i < 2; i++ {
			_, eta := compute(aim, chosen, lift)
			aim = w.clampPitch(mate.Pos.Add(mate.Vel.Scale(eta)))
		}
	}
	pw, eta := compute(aim, chosen, lift)
	if pw > 1 && style == "auto" && chosen == "ground" { // too far to roll: chip it
		chosen = "lob"
		pw, eta = compute(aim, chosen, lift)
	}
	if pw > 1 {
		return nil, gerr("too_far", "%.0f units is beyond a %s pass (max power); pick a nearer target or style lob with higher lift", aim.Sub(ball).Len(), chosen)
	}
	pw = clamp(pw, 0, 1)
	lf := 0.0
	if chosen == "lob" {
		lf = lift
	}
	dir := aim.Sub(ball).Norm()
	p.KickCD = KickCooldown
	p.Face = dir
	w.strike(p, dir, pw, lf)
	// the strike adds 30% of the kicker's own speed; nothing else disturbs the ball
	res := &PassResult{Dir: dir, Power: round1(pw*100) / 100, Lift: lf, Style: chosen, Aim: aim, Dist: round1(aim.Sub(ball).Len()), ETA: round1(eta*10) / 10}
	if _, od := w.nearestOpp(p.Team, aim); od < 5 {
		res.Risk = fmt.Sprintf("an opponent stands %.1f from the target", od)
	}
	if mate != nil && receive && (mate.Agent == nil || mate.auto) {
		mate.order = &Order{Action: "move", Target: aim, Speed: 1, Until: w.Tick + int((eta+1.5)*TickRate), AgentID: a.ID}
		if d := aim.Sub(mate.Pos).Len(); d > 1 {
			if need := d / (math.Max(eta, 0.3) * botMaxSpeed()); need <= 1 {
				mate.order.Speed = math.Max(0.15, need)
				res.Receive = fmt.Sprintf("%s runs to the spot and arrives with the ball", mate.ID)
			} else {
				res.Receive = fmt.Sprintf("%s is sent to the spot but needs %.1fs, the ball takes %.1fs", mate.ID, d/botMaxSpeed(), eta)
			}
		}
	}
	w.emit("pass", map[string]any{"agent": a.ID, "from": p.ID, "to": targetID, "aim": aim, "eta": res.ETA, "style": chosen, "power": res.Power, "lift": lf})
	return res, nil
}

// OrderInfo is the read-only view of a player's active order.
type OrderInfo struct {
	Action, TargetID string
	Target, Offset   Vec
	TTL, ETA         float64
}

// ActiveOrder returns the player's current visible order (coasting is not shown), or nil.
func (p *Player) ActiveOrder(tick int) *OrderInfo {
	o := p.order
	if o == nil || tick >= o.Until || o.Action == "coast" {
		return nil
	}
	return &OrderInfo{Action: o.Action, TargetID: o.TargetID, Target: o.Target, Offset: o.Offset, TTL: float64(o.Until-tick) * Dt, ETA: o.ETA}
}
