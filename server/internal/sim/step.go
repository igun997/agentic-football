package sim

import "math"

// Step advances the world by one fixed tick.
func (w *World) Step() {
	w.Tick++
	switch w.Phase {
	case PhaseLobby, PhaseFinished:
		return
	case PhaseCountdown:
		w.PhaseLeft -= Dt
		if w.PhaseLeft <= 0 {
			w.setPhase(PhasePlaying, 0)
			w.setRestart("kickoff", 0, Vec{})
		}
		return
	case PhaseGoal:
		w.stepBodies(false)
		w.PhaseLeft -= Dt
		if w.PhaseLeft <= 0 {
			w.layout()
			w.Projs, w.Magnets, w.Walls = nil, nil, nil
			if w.TimeLeft <= 0 {
				w.finish()
			} else {
				w.setPhase(PhasePlaying, 0)
				w.setRestart("kickoff", w.kickoffTeam, Vec{})
			}
		}
		return
	}

	w.TimeLeft -= Dt
	w.updateAgents()
	w.updatePlayers()
	w.thinkBots()
	w.updateMagnets()
	w.updateProjectiles()
	w.stepBodies(true)
	w.stepRestart()
	if w.checkGoal() {
		return
	}
	w.checkOut()
	if w.TimeLeft <= 0 {
		w.TimeLeft = 0
		w.finish()
	}
}

func (w *World) finish() {
	switch {
	case w.Score[0] > w.Score[1]:
		w.Winner = 0
	case w.Score[1] > w.Score[0]:
		w.Winner = 1
	default:
		w.Winner = -1
	}
	w.Restart = nil
	w.setPhase(PhaseFinished, 0)
	w.emit("final", map[string]any{"winner": w.Winner, "score": w.Score})
}

func dec(v *float64) {
	if *v > 0 {
		*v = math.Max(0, *v-Dt)
	}
}

func (w *World) updateAgents() {
	for _, a := range w.Agents {
		dec(&a.CompileLeft)
		dec(&a.RecoilLeft)
		dec(&a.SwitchCD)
		for i := range a.SkillCD {
			dec(&a.SkillCD[i])
		}
		if a.MessageTick > 0 && w.Tick-a.MessageTick > 6*TickRate {
			a.Message = ""
		}
	}
}

func (w *World) updatePlayers() {
	for _, p := range w.Players {
		dec(&p.KickCD)
		dec(&p.DashCD)
		dec(&p.Frozen)
		dec(&p.Slowed)
		dec(&p.Hasted)
		dec(&p.Shield)
		dec(&p.botRest)
	}
}

func (p *Player) speedMult() float64 {
	m := 1.0
	if p.Hasted > 0 {
		m *= 1.6
	}
	if p.Slowed > 0 {
		m *= 0.4
	}
	if a := p.Agent; a != nil {
		if a.CompileLeft > 0 {
			m *= 0.35 // the cost of authoring a skill mid-match
		}
		if a.RecoilLeft > 0 {
			m *= 0.6
		}
	}
	if p.Agent == nil || p.auto {
		m *= BotSpeedFactor
	}
	return m
}

func (w *World) updateMagnets() {
	alive := w.Magnets[:0]
	for _, m := range w.Magnets {
		m.Left -= Dt
		if m.Left <= 0 {
			continue
		}
		alive = append(alive, m)
		if o := w.PlayerByID(m.Owner); o != nil && w.Restart == nil {
			d := o.Pos.Sub(w.Ball.Pos)
			if d.Len() <= m.Radius {
				w.Ball.Vel = w.Ball.Vel.Add(d.Norm().Scale(m.Strength * Dt))
				w.touch(o.Team)
			}
		}
	}
	w.Magnets = alive
}

func (w *World) updateProjectiles() {
	alive := w.Projs[:0]
	for _, pr := range w.Projs {
		pr.Life -= Dt
		pr.Pos = pr.Pos.Add(pr.Vel.Scale(Dt))
		hit := pr.Life <= 0 || math.Abs(pr.Pos.X) > FieldHalfX || math.Abs(pr.Pos.Z) > FieldHalfZ
		var victim *Player
		if !hit {
			for _, pl := range w.Players {
				if pl.Team != pr.Team && pl.Pos.Sub(pr.Pos).Len() <= pr.Radius+PlayerRadius {
					victim, hit = pl, true
					break
				}
			}
		}
		if !hit && w.Ball.Pos.Sub(pr.Pos).Len() <= pr.Radius+BallRadius {
			hit = true
		}
		if !hit {
			alive = append(alive, pr)
			continue
		}
		if pr.Life > 0 { // real impact, not mere expiry
			hit := map[string]any{"skill": pr.SkillID, "pos": pr.Pos, "owner": pr.Owner}
			if victim != nil {
				hit["victim"] = who(victim)
			}
			w.emit("projectile_hit", hit)
			w.notes = nil
			if caster := w.PlayerByID(pr.Owner); caster != nil {
				for i := range pr.OnHit {
					e := pr.OnHit[i]
					if e.Type == "status" {
						if e.Target == "self" {
							w.applyStatus(caster, caster, e.Status, e.Duration)
						} else {
							if victim != nil {
								w.applyStatus(victim, caster, e.Status, e.Duration)
							}
							for _, o := range w.enemiesIn(caster, pr.Pos, e.Radius) {
								if o != victim {
									w.applyStatus(o, caster, e.Status, e.Duration)
								}
							}
						}
					} else {
						w.blast(caster, &e, pr.Pos)
					}
				}
				notes := []string{}
				if victim != nil {
					notes = append(notes, "hit "+who(victim))
				} else {
					notes = append(notes, "hit the ball")
				}
				w.emit("skill_result", map[string]any{"player": pr.AgentID, "skill": pr.SkillID, "name": pr.Name, "notes": append(notes, w.notes...)})
			}
		}
	}
	w.Projs = alive
	w.notes = nil
}

type body struct {
	pos *Vec
	vel *Vec
	r   float64
	m   float64
}

// collide resolves overlap + impulse between two circles with restitution e.
func collide(a, b body, e float64) {
	d := b.pos.Sub(*a.pos)
	dist := d.Len()
	minD := a.r + b.r
	if dist >= minD {
		return
	}
	n := d.Norm()
	if dist < 1e-9 {
		n = Vec{1, 0}
	}
	inv := 1/a.m + 1/b.m
	over := minD - dist
	*a.pos = a.pos.Sub(n.Scale(over * (1 / a.m) / inv))
	*b.pos = b.pos.Add(n.Scale(over * (1 / b.m) / inv))
	rv := b.vel.Sub(*a.vel).Dot(n)
	if rv > 0 {
		return
	}
	j := -(1 + e) * rv / inv
	*a.vel = a.vel.Sub(n.Scale(j / a.m))
	*b.vel = b.vel.Add(n.Scale(j / b.m))
}

func closestOnSegment(a, b, p Vec) Vec {
	ab := b.Sub(a)
	l2 := ab.Dot(ab)
	if l2 == 0 {
		return a
	}
	t := clamp(p.Sub(a).Dot(ab)/l2, 0, 1)
	return a.Add(ab.Scale(t))
}

func collideWall(pos, vel *Vec, r float64, wl *Wall, e float64) {
	c := closestOnSegment(wl.A, wl.B, *pos)
	d := pos.Sub(c)
	dist := d.Len()
	if dist >= r {
		return
	}
	n := d.Norm()
	if dist < 1e-9 {
		n = Vec{-(wl.B.Z - wl.A.Z), wl.B.X - wl.A.X}.Norm()
	}
	*pos = c.Add(n.Scale(r))
	if v := vel.Dot(n); v < 0 {
		*vel = vel.Sub(n.Scale((1 + e) * v))
	}
}

func (w *World) mass(p *Player) float64 {
	if p.Frozen > 0 {
		return 20
	}
	return 4
}

func (w *World) stepBodies(controlled bool) {
	for _, p := range w.Players {
		desired := Vec{}
		if controlled && p.Frozen <= 0 {
			desired = p.Move.Scale(PlayerMaxSpeed * p.speedMult())
		}
		delta := desired.Sub(p.Vel)
		if l := delta.Len(); l > PlayerAccel*Dt {
			delta = delta.Scale(PlayerAccel * Dt / l)
		}
		p.Vel = p.Vel.Add(delta)
		p.Pos = p.Pos.Add(p.Vel.Scale(Dt))
	}
	drag := 0.975 // rolling friction
	if w.Ball.Y > 0 || w.Ball.VY != 0 {
		drag = 0.996 // little drag in the air
		w.Ball.VY -= Gravity * Dt
		w.Ball.Y += w.Ball.VY * Dt
		if w.Ball.Y <= 0 {
			w.Ball.Y = 0
			if w.Ball.VY < -4 {
				w.Ball.VY = -w.Ball.VY * BounceE // bounce
			} else {
				w.Ball.VY = 0
			}
		}
	}
	w.Ball.Vel = w.Ball.Vel.Scale(drag)
	w.Ball.Pos = w.Ball.Pos.Add(w.Ball.Vel.Scale(Dt))

	n := len(w.Players)
	for i := 0; i < n; i++ {
		a := w.Players[i]
		for j := i + 1; j < n; j++ {
			b := w.Players[j]
			if a.Pos.Sub(b.Pos).Len() < 2*PlayerRadius {
				collide(body{&a.Pos, &a.Vel, PlayerRadius, w.mass(a)}, body{&b.Pos, &b.Vel, PlayerRadius, w.mass(b)}, 0.5)
			}
		}
		airborne := w.Ball.Y > AirReach // a lofted ball flies over the discs
		if !airborne {
			collide(body{&a.Pos, &a.Vel, PlayerRadius, w.mass(a)}, body{&w.Ball.Pos, &w.Ball.Vel, BallRadius, 1}, 0.8)
		}
		if !airborne && w.Restart == nil && a.Pos.Sub(w.Ball.Pos).Len() <= PlayerRadius+BallRadius+0.05 {
			w.touch(a.Team)
			w.autoControl(a)
		}
	}
	for _, wl := range w.Walls {
		wl.Left -= Dt
		for _, p := range w.Players {
			collideWall(&p.Pos, &p.Vel, PlayerRadius, wl, 0.2)
		}
		if w.Ball.Y < WallH {
			collideWall(&w.Ball.Pos, &w.Ball.Vel, BallRadius, wl, 0.9)
		}
	}
	alive := w.Walls[:0]
	for _, wl := range w.Walls {
		if wl.Left > 0 {
			alive = append(alive, wl)
		}
	}
	w.Walls = alive

	if r := w.Restart; r != nil { // opponents keep their distance from a set piece
		for _, p := range w.Players {
			if p.Team != r.Team {
				if d := p.Pos.Sub(r.Spot); d.Len() < r.Keepout {
					dir := d.Norm()
					if dir == (Vec{}) {
						dir = Vec{1, 0}
					}
					p.Pos = r.Spot.Add(dir.Scale(r.Keepout))
				}
			}
		}
	}
	for _, p := range w.Players {
		p.Pos.X = clamp(p.Pos.X, -FieldHalfX+PlayerRadius, FieldHalfX-PlayerRadius)
		p.Pos.Z = clamp(p.Pos.Z, -FieldHalfZ+PlayerRadius, FieldHalfZ-PlayerRadius)
	}
	w.ballBounds()
}

// ballBounds only bounces the ball inside the goal nets and off the posts; everywhere else
// the ball is free to leave the pitch (that triggers a throw-in, corner or goal kick).
func (w *World) ballBounds() {
	b := &w.Ball
	const e = 0.8
	for _, sx := range []float64{-1, 1} {
		for _, sz := range []float64{-1, 1} {
			if b.Y > WallH {
				continue
			}
			post := Vec{sx * FieldHalfX, sz * GoalHalfWidth}
			collide(body{&post, &Vec{}, PostRadius, 1e9}, body{&b.Pos, &b.Vel, BallRadius, 1}, 0.8)
		}
	}
	if math.Abs(b.Pos.Z) < GoalHalfWidth && math.Abs(b.Pos.X) > FieldHalfX { // inside a net
		if b.Pos.Z+BallRadius > GoalHalfWidth {
			b.Pos.Z, b.Vel.Z = GoalHalfWidth-BallRadius, -math.Abs(b.Vel.Z)*e
		} else if b.Pos.Z-BallRadius < -GoalHalfWidth {
			b.Pos.Z, b.Vel.Z = -GoalHalfWidth+BallRadius, math.Abs(b.Vel.Z)*e
		}
		limit := FieldHalfX + GoalDepth
		if b.Pos.X+BallRadius > limit {
			b.Pos.X, b.Vel.X = limit-BallRadius, -math.Abs(b.Vel.X)*e
		} else if b.Pos.X-BallRadius < -limit {
			b.Pos.X, b.Vel.X = -limit+BallRadius, math.Abs(b.Vel.X)*e
		}
	}
}

// checkGoal scores when the ball centre crosses a goal line inside the mouth.
func (w *World) checkGoal() bool {
	b := w.Ball.Pos
	if math.Abs(b.Z) >= GoalHalfWidth-0.2 || math.Abs(b.X) <= FieldHalfX || w.Ball.Y > CrossbarY {
		return false // outside the posts, or over the bar
	}
	team := 0 // right goal => team 0 scores
	if b.X < 0 {
		team = 1
	}
	w.Score[team]++
	w.kickoffTeam = 1 - team
	var scorer string
	bestD := math.MaxFloat64
	for _, p := range w.Players {
		if p.Team == team && p.Pos.Sub(b).Len() < bestD {
			bestD, scorer = p.Pos.Sub(b).Len(), p.ID
		}
	}
	name := scorer
	if pl := w.PlayerByID(scorer); pl != nil {
		name = scorerLabel(pl)
	}
	w.Restart = nil
	w.emit("goal", map[string]any{"team": team, "score": w.Score, "scorer": scorer, "label": name})
	w.setPhase(PhaseGoal, GoalPauseSecs)
	return true
}

func scorerLabel(p *Player) string {
	if p.Agent != nil {
		return p.Agent.Name
	}
	return "#" + itoa(p.Num)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// checkOut implements throw-ins, corners and goal kicks.
func (w *World) checkOut() {
	if w.Restart != nil {
		return
	}
	b := w.Ball.Pos
	last := w.LastTouchTeam
	switch {
	case math.Abs(b.Z) > FieldHalfZ:
		team := 1 - last
		if last < 0 {
			team = 0
		}
		w.setRestart("throw_in", team, Vec{clamp(b.X, -FieldHalfX+3, FieldHalfX-3), sign(b.Z) * (FieldHalfZ - 4)}) // spot sits inside so the taker can stand outside it
	case math.Abs(b.X) > FieldHalfX && (math.Abs(b.Z) >= GoalHalfWidth-0.2 || w.Ball.Y > CrossbarY):
		def := 0 // ball crossed the left end line => team 0's goal line
		if b.X > 0 {
			def = 1
		}
		if last == def {
			w.setRestart("corner", 1-def, Vec{sign(b.X) * (FieldHalfX - 3.6), sign(b.Z) * (FieldHalfZ - 3.6)})
		} else {
			w.setRestart("goal_kick", def, Vec{sign(b.X) * (FieldHalfX - 6), sign(b.Z) * 4})
		}
	}
}

// stepRestart pins the ball on its spot, then lets a bot of the taking team take the set piece.
func (w *World) stepRestart() {
	r := w.Restart
	if r == nil {
		return
	}
	w.Ball = Ball{Pos: r.Spot}
	if r.Wait -= Dt; r.Wait > 0 {
		return
	}
	taker := w.pickTaker(r)
	if taker == nil { // every candidate is agent-controlled: let play continue
		w.Restart = nil
		return
	}
	aim, speed := w.restartAim(taker, r)
	taker.Pos = r.Spot.Sub(aim.Scale(PlayerRadius + BallRadius + 0.3))
	taker.Pos.X = clamp(taker.Pos.X, -FieldHalfX+PlayerRadius, FieldHalfX-PlayerRadius)
	taker.Pos.Z = clamp(taker.Pos.Z, -FieldHalfZ+PlayerRadius, FieldHalfZ-PlayerRadius)
	taker.Vel, taker.Face = Vec{}, aim
	w.Ball.Vel = aim.Scale(speed)
	w.touch(taker.Team)
	w.Restart = nil
	w.emit("restart_taken", map[string]any{"kind": r.Kind, "team": r.Team, "player": taker.ID})
}

func (w *World) pickTaker(r *Restart) *Player {
	var best *Player
	bestD := math.MaxFloat64
	for _, p := range w.Players {
		if p.Team != r.Team || !p.auto || (p.Role == RoleGK) != (r.Kind == "goal_kick") {
			continue // the keeper takes goal kicks and nothing else
		}
		if d := p.Pos.Sub(r.Spot).Len(); d < bestD {
			bestD, best = d, p
		}
	}
	return best
}

func (w *World) restartAim(taker *Player, r *Restart) (Vec, float64) {
	atk := attackDir(r.Team)
	switch r.Kind {
	case "corner":
		return Vec{atk * (FieldHalfX - 10), 0}.Sub(r.Spot).Norm(), 30
	case "goal_kick":
		return Vec{0, -sign(r.Spot.Z) * 14}.Sub(r.Spot).Norm(), 32
	}
	if mate := w.bestPassTarget(taker, r.Spot, true); mate != nil {
		d := mate.Pos.Sub(r.Spot)
		return d.Norm(), clamp(6+d.Len()*0.8, 10, 30)
	}
	return Vec{atk, 0}, 14
}

func attackDir(team int) float64 {
	if team == 0 {
		return 1
	}
	return -1
}
