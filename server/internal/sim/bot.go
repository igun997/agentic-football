package sim

import (
	"math"
	"sort"
)

// Bot AI for every player that no agent drives (and for an idle agent's player: autopilot).
// It plays a recognisable football game: the team in possession spreads and runs, the defending
// team compresses and marks goal-side, one player challenges for the ball, the keeper reads shots,
// passes are chosen by progress / openness / receiver space and are weighted to arrive, and under
// pressure the ball is chipped or cleared wide. Deterministic (seeded rng).

// SetSeed makes bot randomness reproducible (tests, tuning).
func (w *World) SetSeed(seed int64) { w.rng.Seed(seed) }

func (w *World) thinkBots() {
	for _, p := range w.Players {
		p.auto = p.Agent == nil || (w.Autopilot && w.Tick-p.Agent.LastCmdTick > AutopilotAfter)
	}
	poss := w.possession()
	var chaser, presser [2]*Player
	for team := 0; team < 2; team++ {
		type cand struct {
			p *Player
			d float64
		}
		var cs []cand
		agentNear := false
		bp := w.Ball.Pos.Add(w.Ball.Vel.Scale(0.2))
		for _, p := range w.Players {
			if p.Team != team {
				continue
			}
			d := p.Pos.Sub(bp).Len()
			if !p.auto {
				if d < 7 {
					agentNear = true
				}
				continue
			}
			if p.Role == RoleGK || p.Frozen > 0 || p.botRest > 0 {
				continue
			}
			cs = append(cs, cand{p, d})
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].d < cs[j].d })
		if agentNear || w.Restart != nil || w.Ball.Y > 2.5 && len(cs) > 0 && cs[0].d > 6 {
			continue // a driven teammate is on it / dead ball / ball well overhead
		}
		if len(cs) > 0 {
			chaser[team] = cs[0].p
		}
		// the side without the ball presses with a second player when the ball is dangerous
		if poss != team && len(cs) > 1 && (w.Tactics[team].Press || math.Abs(w.Ball.Pos.X-(-attackDir(team)*FieldHalfX)) < 34) {
			presser[team] = cs[1].p
		}
	}
	marks := [2]map[*Player]*Player{}
	for team := 0; team < 2; team++ {
		if poss != team {
			marks[team] = w.assignMarks(team, chaser[team], presser[team])
		}
	}
	for _, p := range w.Players {
		if !p.auto {
			continue
		}
		if p.Frozen > 0 {
			p.Move = Vec{}
			continue
		}
		if p.order != nil && w.runOrder(p) {
			continue // the agent's orders override the bot's own decisions
		}
		switch {
		case p.Role == RoleGK:
			w.thinkKeeper(p)
		case chaser[p.Team] == p:
			w.thinkChaser(p, poss == p.Team)
		case presser[p.Team] == p:
			w.thinkPresser(p)
		default:
			w.thinkShape(p, poss == p.Team, marks[p.Team][p])
		}
	}
}

// possession is the team that last touched the ball, or the nearer team when it is loose.
func (w *World) possession() int {
	if w.LastTouchTeam >= 0 {
		return w.LastTouchTeam
	}
	best, team := math.MaxFloat64, 0
	for _, p := range w.Players {
		if d := p.Pos.Sub(w.Ball.Pos).Len(); d < best {
			best, team = d, p.Team
		}
	}
	return team
}

func (w *World) moveTo(p *Player, target Vec) {
	d := target.Sub(p.Pos)
	l := d.Len()
	if l < 1.2 {
		p.Move = Vec{}
		return
	}
	p.Move = d.Scale(1 / l).Scale(math.Min(1, l/5))
	p.Face = d.Norm()
}

func (w *World) nearestOpp(team int, pos Vec) (*Player, float64) {
	var best *Player
	bd := math.MaxFloat64
	for _, o := range w.Players {
		if o.Team == team {
			continue
		}
		if d := o.Pos.Sub(pos).Len(); d < bd {
			bd, best = d, o
		}
	}
	return best, bd
}

// assignMarks pairs defending bots with the most dangerous opposing outfielders (greedy, goal-side).
func (w *World) assignMarks(team int, skip ...*Player) map[*Player]*Player {
	own := Vec{-attackDir(team) * FieldHalfX, 0}
	var threats []*Player
	for _, o := range w.Players {
		if o.Team != team && o.Role != RoleGK && o.Pos.Sub(w.Ball.Pos).Len() > 5 {
			threats = append(threats, o)
		}
	}
	sort.Slice(threats, func(i, j int) bool { return threats[i].Pos.Sub(own).Len() < threats[j].Pos.Sub(own).Len() })
	taken := map[*Player]bool{}
	out := map[*Player]*Player{}
	for _, s := range skip {
		if s != nil {
			taken[s] = true
		}
	}
	for _, th := range threats {
		var best *Player
		bd := 22.0 // only mark within a zone
		for _, d := range w.Players {
			if d.Team != team || d.Role == RoleGK || d.Role == RoleFWD || taken[d] || !d.auto {
				continue
			}
			if dist := d.Pos.Sub(th.Pos).Len(); dist < bd {
				bd, best = dist, d
			}
		}
		if best != nil {
			taken[best] = true
			out[best] = th
		}
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func (w *World) styleShift(p *Player) (push, mult float64) {
	mult = 1
	switch w.Tactics[p.Team].Line {
	case "high":
		push = 6
	case "low":
		push = -6
	}
	if p.Agent != nil && p.Agent.Brief != nil { // an idle agent's body plays in its trainer's style
		switch p.Agent.Brief.Persona.Style {
		case "aggressive":
			return push + 4, 1.2
		case "defensive":
			return push - 4, 0.8
		}
	}
	return push, 1
}

func (w *World) thinkShape(p *Player, attacking bool, mark *Player) {
	b := w.Ball.Pos
	home := p.Home
	atk := attackDir(p.Team)
	own := Vec{-atk * FieldHalfX, 0}
	push, mult := w.styleShift(p)
	var t Vec
	if mark != nil { // goal-side marking
		t = mark.Pos.Add(own.Sub(mark.Pos).Norm().Scale(3.6))
	} else if attacking {
		switch p.Role {
		case RoleFWD: // run beyond the ball and stretch the pitch
			t = Vec{b.X + atk*(10+push), home.Z*1.25 + b.Z*0.15}
		case RoleMID:
			t = Vec{home.X + (b.X-home.X)*0.7*mult + atk*(4+push), home.Z + b.Z*0.25}
		default:
			t = Vec{home.X + (b.X-home.X)*0.4*mult + atk*(3+push), home.Z + b.Z*0.2}
			t.X = clampSide(t.X, atk, -FieldHalfX+8, 6)
		}
	} else {
		switch p.Role {
		case RoleFWD: // drop in and cut passing lanes
			t = Vec{home.X*0.7 + b.X*0.3 + atk*push, home.Z*0.8 + b.Z*0.3}
			t.X = clampSide(t.X, atk, -22, 8)
		case RoleMID:
			t = Vec{home.X + (b.X-home.X)*0.5*mult + atk*push, home.Z*0.8 + b.Z*0.45}
		default: // back line steps up and squeezes
			t = Vec{home.X + (b.X-home.X)*0.3*mult + atk*push, home.Z*0.7 + b.Z*0.4}
			t.X = clampSide(t.X, atk, -FieldHalfX+7, 2)
		}
	}
	// keep distance from team-mates so the team does not clump
	for _, m := range w.Players {
		if m == p || m.Team != p.Team {
			continue
		}
		if dv := p.Pos.Sub(m.Pos); dv.Len() < 7 && dv.Len() > 0.01 {
			t = t.Add(dv.Norm().Scale((7 - dv.Len()) * 0.7))
		}
	}
	switch w.Tactics[p.Team].Width {
	case "wide":
		t.Z *= 1.3
	case "narrow":
		t.Z *= 0.7
	}
	t.X = clamp(t.X, -FieldHalfX+PlayerRadius+1, FieldHalfX-PlayerRadius-1)
	t.Z = clamp(t.Z, -FieldHalfZ+PlayerRadius+1, FieldHalfZ-PlayerRadius-1)
	w.moveTo(p, t)
}

// clampSide limits x to [lo, hi] measured in the team's attacking direction.
func clampSide(x, atk, lo, hi float64) float64 {
	if atk > 0 {
		return clamp(x, lo, hi)
	}
	return clamp(x, -hi, -lo)
}

func (w *World) thinkPresser(p *Player) {
	own := Vec{-attackDir(p.Team) * FieldHalfX, 0}
	bp := w.Ball.Pos.Add(w.Ball.Vel.Scale(0.3))
	w.moveTo(p, bp.Add(own.Sub(bp).Norm().Scale(4))) // stand goal-side of the ball and delay
}

func (w *World) thinkKeeper(p *Player) {
	atk := attackDir(p.Team)
	own := -atk * FieldHalfX
	ball := w.Ball.Pos
	line := own + atk*2.8
	// shot coming: track the line of the ball to the goal
	if w.Restart == nil && math.Abs(w.Ball.Vel.X) > 8 && w.Ball.Vel.X*(-atk) > 0 && math.Abs(ball.X-own) < 34 {
		t := (own - ball.X) / w.Ball.Vel.X
		if t > 0 && t < 0.9 { // keepers only react to the shot late, and never read it perfectly
			zp := clamp(ball.Z+w.Ball.Vel.Z*t+(w.rng.Float64()-0.5)*6, -GoalHalfWidth+1.2, GoalHalfWidth-1.2)
			w.moveTo(p, Vec{line, zp})
			if w.Ball.Y < 1.2 && w.Ball.Pos.Sub(p.Pos).Len() <= PlayerRadius+BallRadius+0.8 { // only a real save, not a long arm
				w.botKick(p, w.clearDir(p, ball), 0.9, 0.5)
			}
			return
		}
	}
	inBox := math.Abs(ball.X-own) < 17 && math.Abs(ball.Z) < 19
	if inBox && w.Restart == nil {
		_, oppD := w.nearestOpp(p.Team, ball)
		myD := p.Pos.Sub(ball).Len()
		if myD < oppD*1.2 || myD < 7 { // loose ball in the area: come and claim it
			bp := ball.Add(w.Ball.Vel.Scale(0.25))
			w.moveTo(p, bp)
			if w.inReach(p) {
				if _, d := w.nearestOpp(p.Team, p.Pos); d < 8 {
					w.botKick(p, w.clearDir(p, ball), 0.9, 0.55) // pressed: hoof it wide
				} else if m := w.bestPass(p, ball, true); m.who != nil {
					w.passTo(p, m, ball)
				} else {
					w.botKick(p, w.clearDir(p, ball), 0.8, 0.4)
				}
			}
			return
		}
		w.moveTo(p, Vec{own + atk*3.6, clamp(ball.Z*0.45, -GoalHalfWidth+1.5, GoalHalfWidth-1.5)})
		return
	}
	w.moveTo(p, Vec{line, clamp(ball.Z*0.3, -GoalHalfWidth+1.5, GoalHalfWidth-1.5)})
}

// clearDir is a safe clearance direction: upfield and towards the emptier flank.
func (w *World) clearDir(p *Player, ball Vec) Vec {
	atk := attackDir(p.Team)
	left, right := 0, 0
	for _, o := range w.Players {
		if o.Team != p.Team {
			if o.Pos.Z < ball.Z {
				left++
			} else {
				right++
			}
		}
	}
	side := 1.0
	if right > left {
		side = -1
	}
	return Vec{atk * 28, side * 18}.Add(Vec{0, -ball.Z * 0.2}).Norm()
}

func (w *World) botKick(p *Player, d Vec, power, lift float64) {
	if p.KickCD > 0 || !w.inReach(p) {
		return
	}
	if p.Agent == nil && w.AutoControl { // the ball reached a free player: an agent takes him over instead of the bot acting
		if w.autoControl(p); p.Agent != nil {
			return
		}
	}
	spread := 0.12
	if w.shooting {
		spread = 0.035 // finishing is accurate
	}
	jitter := (w.rng.Float64() - 0.5) * spread
	c, s := math.Cos(jitter), math.Sin(jitter)
	d = Vec{d.X*c - d.Z*s, d.X*s + d.Z*c}
	p.KickCD = KickCooldown
	p.botRest = 0.7
	p.Face = d
	w.strike(p, d, power, lift)
}

// blocked reports whether an opponent stands on the ball's path d (unit) over the next `length` units.
func (w *World) blocked(team int, from, d Vec, length float64) bool {
	end := from.Add(d.Scale(length))
	for _, o := range w.Players {
		if o.Team == team || o.Role == RoleGK {
			continue
		}
		c := closestOnSegment(from.Add(d.Scale(2.5)), end, o.Pos)
		if c.Sub(o.Pos).Len() < PlayerRadius+BallRadius+0.8 {
			return true
		}
	}
	return false
}

// clearLane rotates the wanted direction until the ball's path is free of opponents.
func (w *World) clearLane(team int, from, want Vec, length float64) (Vec, bool) {
	for _, a := range []float64{0, 0.35, -0.35, 0.7, -0.7, 1.1, -1.1} {
		c, s := math.Cos(a), math.Sin(a)
		d := Vec{want.X*c - want.Z*s, want.X*s + want.Z*c}
		if !w.blocked(team, from, d, length) {
			return d, true
		}
	}
	return want, false
}

func (w *World) thinkChaser(p *Player, attacking bool) {
	atk := attackDir(p.Team)
	own := Vec{-atk * FieldHalfX, 0}
	goal := Vec{atk * FieldHalfX, 0}
	bp := w.Ball.Pos.Add(w.Ball.Vel.Scale(0.2))
	if w.inReach(p) {
		w.decideKick(p)
		return
	}
	if attacking {
		u := goal.Sub(bp).Norm()
		w.moveTo(p, bp.Sub(u.Scale(PlayerRadius+BallRadius+1.0))) // get goal-side of the ball, then touch it
	} else {
		w.moveTo(p, bp.Add(own.Sub(bp).Norm().Scale(1.5))) // intercept
	}
}

// decideKick is what a bot does with the ball at its feet: shoot, pass, carry or clear.
func (w *World) decideKick(p *Player) {
	atk := attackDir(p.Team)
	ball := w.Ball.Pos
	goal := Vec{atk * FieldHalfX, 0}
	dGoal := goal.Sub(ball).Len()
	_, oppD := w.nearestOpp(p.Team, p.Pos)
	pressed := oppD < 7

	// 1. shoot from range, away from the keeper (sometimes chipped when the keeper is off his line)
	if dGoal < 40 && ball.X*atk > 10 {
		var gk *Player
		for _, o := range w.Players {
			if o.Team != p.Team && o.Role == RoleGK {
				gk = o
			}
		}
		gz := 0.0
		if gk != nil {
			gz = gk.Pos.Z
		}
		tz := -sign(gz+0.01) * (3.6 + w.rng.Float64()*1.4) // aim for the corner, away from the keeper
		dir := Vec{goal.X, tz}.Sub(ball).Norm()
		lift := 0.0
		if gk != nil && math.Abs(gk.Pos.X-goal.X) > 6 && w.rng.Float64() < 0.5 {
			lift = 0.45 // keeper rushed out: chip him
		}
		if d, ok := w.clearLane(p.Team, ball, dir, dGoal); ok {
			w.Stats["shot"]++
			w.shooting = true
			w.botKick(p, d, 1, lift)
			w.shooting = false
			return
		}
		w.Stats["shot_blocked"]++
	}
	// 2. pass to the best-placed team-mate
	thr := 3.0
	if pressed {
		thr = -6
	}
	if m := w.bestPass(p, ball, pressed); m.who != nil && m.score > thr {
		w.Stats["pass"]++
		w.passTo(p, m, ball)
		return
	}
	// 3. carry the ball into space, or clear it when pressed deep in our own half
	if !pressed {
		if d, ok := w.clearLane(p.Team, ball, goal.Sub(ball).Norm(), 14); ok {
			w.Stats["carry"]++
			w.botKick(p, d, 0.2, 0)
			return
		}
	}
	if ball.X*atk < -16 {
		w.Stats["clear"]++
		w.botKick(p, w.clearDir(p, ball), 0.85, 0.55)
		return
	}
	w.botKick(p, Vec{atk, sign(-ball.Z) * 0.6}.Norm(), 0.3, 0) // shield and knock it into room
}

type passChoice struct {
	who   *Player
	score float64
	lob   bool
}

// bestPass scores every team-mate: progress, distance, openness of the lane and space around the receiver.
func (w *World) bestPass(from *Player, at Vec, allowBack bool) passChoice {
	atk := attackDir(from.Team)
	best := passChoice{score: -1e9}
	for _, m := range w.Players {
		if m == from || m.Team != from.Team {
			continue
		}
		if m.Role == RoleGK && !(allowBack && at.X*atk < -26) {
			continue
		}
		aim := m.Pos.Add(m.Vel.Scale(0.4))
		d := aim.Sub(at)
		dist := d.Len()
		if dist < 6 || dist > 40 {
			continue
		}
		progress := d.X * atk
		if progress < 0 && !allowBack {
			continue
		}
		score := progress*0.9 - 0.12*dist
		if aim.X*atk > 20 {
			score += 4
		}
		if math.Abs(aim.Z) > 25 {
			score -= 4
		}
		if _, od := w.nearestOpp(from.Team, aim); od < 5 {
			score -= 8
		} else {
			score += math.Min(6, (od-5)*0.5)
		}
		lob := false
		if w.blocked(from.Team, at, d.Norm(), dist) {
			if dist > 12 {
				lob, score = true, score-6 // chip it over the marker, a bit riskier
			} else {
				score -= 40
			}
		}
		if score > best.score {
			best = passChoice{who: m, score: score, lob: lob}
		}
	}
	return best
}

// passTo plays the ball to the chosen team-mate with the power that makes it arrive.
func (w *World) passTo(p *Player, c passChoice, from Vec) {
	aim := c.who.Pos.Add(c.who.Vel.Scale(0.4))
	d := aim.Sub(from)
	dist := d.Len()
	if c.lob {
		lift := 0.7
		flight := 2 * (lift * 26) / Gravity
		v := dist / flight * 1.05
		w.botKick(p, d.Norm(), clamp((v/(1-0.3*lift)-12)/38, 0.1, 1), lift)
		return
	}
	v0 := dist*0.75 + 8
	w.botKick(p, d.Norm(), clamp((v0-12)/38, 0.05, 1), 0)
}
