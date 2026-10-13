package sim

import "math"

// Bot AI for every player that no agent controls: hold formation, shift with the ball,
// the nearest bot challenges for the ball, the keeper guards the goal. Deterministic (seeded rng).

var roleShiftX = map[Role]float64{RoleGK: 0, RoleDEF: 0.22, RoleMID: 0.42, RoleFWD: 0.55}
var roleShiftZ = map[Role]float64{RoleGK: 0, RoleDEF: 0.3, RoleMID: 0.4, RoleFWD: 0.4}

func (w *World) thinkBots() {
	for _, p := range w.Players {
		p.auto = p.Agent == nil || (w.Autopilot && w.Tick-p.Agent.LastCmdTick > AutopilotAfter)
	}
	var chaser [2]*Player
	for team := 0; team < 2; team++ {
		best := math.MaxFloat64
		agentNear := false
		for _, p := range w.Players {
			if p.Team != team {
				continue
			}
			d := p.Pos.Sub(w.Ball.Pos).Len()
			if !p.auto {
				if d < 7 {
					agentNear = true
				}
				continue
			}
			if p.Role == RoleGK || p.Frozen > 0 || p.botRest > 0 {
				continue
			}
			if d < best {
				best, chaser[team] = d, p
			}
		}
		if agentNear || w.Restart != nil {
			chaser[team] = nil // a human-driven teammate is on it / dead ball
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
		switch {
		case p.Role == RoleGK:
			w.thinkKeeper(p)
		case chaser[p.Team] == p:
			w.thinkChaser(p)
		default:
			w.thinkShape(p)
		}
	}
}

func (w *World) moveTo(p *Player, target Vec) {
	d := target.Sub(p.Pos)
	l := d.Len()
	if l < 1.2 {
		p.Move = Vec{}
		return
	}
	p.Move = d.Scale(1 / l).Scale(math.Min(1, l/6))
	p.Face = d.Norm()
}

func (w *World) thinkShape(p *Player) {
	b := w.Ball.Pos
	home := p.Home
	atk := attackDir(p.Team)
	shift := roleShiftX[p.Role]
	push := 0.0
	if p.Agent != nil && p.Agent.Brief != nil { // an idle agent's body plays in its trainer's style
		switch p.Agent.Brief.Persona.Style {
		case "aggressive":
			shift, push = shift*1.3, 4
		case "defensive":
			shift, push = shift*0.6, -4
		}
	}
	t := Vec{home.X + (b.X-home.X)*shift + atk*push, home.Z + (b.Z-home.Z)*roleShiftZ[p.Role]}
	switch p.Role {
	case RoleFWD: // make a run: stay ahead of the ball so there is always a pass on
		t.X = b.X + atk*11
	case RoleMID:
		t.X = b.X + atk*3
	case RoleDEF:
		t.X = clampSide(t.X, atk, -FieldHalfX+8, 12)
	}
	t.X = clampSide(t.X, atk, -FieldHalfX+10, FieldHalfX-6)
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

func (w *World) thinkKeeper(p *Player) {
	own := -attackDir(p.Team) * FieldHalfX
	ball := w.Ball.Pos
	distToGoal := math.Abs(ball.X - own)
	if distToGoal < 13 && math.Abs(ball.Z) < 16 && w.Restart == nil {
		if w.inReach(p) {
			// clear it upfield and wide
			w.botKick(p, Vec{attackDir(p.Team) * 20, sign(ball.Z) * 14}.Norm(), 0.9, 0.5)
			return
		}
		w.moveTo(p, ball)
		return
	}
	t := Vec{own + attackDir(p.Team)*2.8, clamp(ball.Z*0.35, -GoalHalfWidth+1.5, GoalHalfWidth-1.5)}
	w.moveTo(p, t)
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
	jitter := (w.rng.Float64() - 0.5) * 0.16
	c, s := math.Cos(jitter), math.Sin(jitter)
	d = Vec{d.X*c - d.Z*s, d.X*s + d.Z*c}
	p.KickCD = KickCooldown
	p.botRest = 0.9
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
		if c.Sub(o.Pos).Len() < PlayerRadius+BallRadius+1 {
			return true
		}
	}
	return false
}

// clearLane rotates the wanted direction until the ball's path is free of opponents.
func (w *World) clearLane(team int, from, want Vec, length float64) (Vec, bool) {
	for _, a := range []float64{0, 0.45, -0.45, 0.9, -0.9, 1.4, -1.4} {
		c, s := math.Cos(a), math.Sin(a)
		d := Vec{want.X*c - want.Z*s, want.X*s + want.Z*c}
		if !w.blocked(team, from, d, length) {
			return d, true
		}
	}
	return want, false
}

func (w *World) thinkChaser(p *Player) {
	atk := attackDir(p.Team)
	goal := Vec{atk * FieldHalfX, 0}
	ball := w.Ball.Pos
	u := goal.Sub(ball).Norm()
	if w.inReach(p) {
		if goal.Sub(ball).Len() < 36 { // shoot, away from the keeper
			keeperZ := 0.0
			for _, o := range w.Players {
				if o.Team != p.Team && o.Role == RoleGK {
					keeperZ = o.Pos.Z
				}
			}
			tz := -sign(keeperZ+0.01) * 4.5
			w.botKick(p, Vec{goal.X, tz}.Sub(ball).Norm(), 1, 0)
			return
		}
		if mate := w.bestPassTarget(p, ball, false); mate != nil {
			d := mate.Pos.Sub(ball)
			if dir, ok := w.clearLane(p.Team, ball, d.Norm(), d.Len()); ok {
				w.botKick(p, dir, clamp((d.Len()*0.8-6)/38, 0.2, 0.95), 0)
				return
			}
			if d := mate.Pos.Sub(ball); d.Len() > 12 { // lane blocked: chip it over the defenders
				w.botKick(p, d.Norm(), clamp((d.Len()*0.7-6)/38, 0.3, 0.9), 0.75)
				return
			}
		}
		// carry it: knock it ahead into free space, hard enough to get away from markers
		if dir, ok := w.clearLane(p.Team, ball, u, 16); ok {
			w.botKick(p, dir, 0.2, 0)
		} else {
			w.botKick(p, dir, 0.7, 0.5) // boxed in: lift it out
		}
		return
	}
	// get goal-side of the ball, then touch it
	w.moveTo(p, ball.Sub(u.Scale(PlayerRadius+BallRadius+1.2)))
}

// bestPassTarget picks a teammate for a pass: progress towards goal, not marked, not too far.
func (w *World) bestPassTarget(from *Player, at Vec, allowBackward bool) *Player {
	atk := attackDir(from.Team)
	var best *Player
	bestScore := -1e9
	for _, m := range w.Players {
		if m == from || m.Team != from.Team || m.Role == RoleGK {
			continue
		}
		d := m.Pos.Sub(at)
		dist := d.Len()
		if dist < 6 || dist > 38 {
			continue
		}
		progress := d.X * atk
		if progress < 2 && !allowBackward {
			continue
		}
		score := progress - 0.35*dist
		for _, o := range w.Players {
			if o.Team != from.Team && o.Pos.Sub(m.Pos).Len() < 6 {
				score -= 14
			}
		}
		if score > bestScore {
			bestScore, best = score, m
		}
	}
	return best
}
