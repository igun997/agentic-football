package sim

import (
	"math"
	"strings"
	"testing"
)

// duel builds a world with one player per team (no bots) so physics tests are deterministic.
func duel(t *testing.T) (*World, *Agent, *Agent) {
	t.Helper()
	w := NewWorld(60, 1)
	w.Autopilot = false // physics tests drive the bodies by hand
	a, b := w.AddAgent("a1", "A", 0), w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	return w, a, b
}

func startPlay(t *testing.T, w *World) {
	t.Helper()
	w.Start()
	for i := 0; i < int((CountdownSecs+RestartWait+0.5)*TickRate); i++ {
		w.Step()
	}
	if w.Phase != PhasePlaying || w.Restart != nil {
		t.Fatalf("not in open play: phase=%s restart=%v", w.Phase, w.Restart)
	}
}

func goodVisual() Visual {
	return Visual{Code: "const m=new THREE.Mesh(new THREE.SphereGeometry(1),new THREE.MeshBasicMaterial({color:ctx.color}));ctx.group.add(m);return {update(t){m.scale.setScalar(1+t)}};", DurationMs: 800}
}

func TestSquadsAreRealFootballTeams(t *testing.T) {
	w := NewWorld(180, 11)
	var gk, def, mid, fwd [2]int
	for _, p := range w.Players {
		switch p.Role {
		case RoleGK:
			gk[p.Team]++
		case RoleDEF:
			def[p.Team]++
		case RoleMID:
			mid[p.Team]++
		case RoleFWD:
			fwd[p.Team]++
		}
		if (p.Team == 0) != (p.Home.X < 0) {
			t.Fatalf("player %s starts in the wrong half: %v", p.ID, p.Home)
		}
	}
	if len(w.Players) != 22 || gk != [2]int{1, 1} || def != [2]int{4, 4} || mid != [2]int{4, 4} || fwd != [2]int{2, 2} {
		t.Fatalf("want 11v11 4-4-2, got n=%d gk=%v def=%v mid=%v fwd=%v", len(w.Players), gk, def, mid, fwd)
	}
	a := w.AddAgent("a1", "A", 0)
	if a.Body == nil || a.Body.Role != RoleFWD {
		t.Fatalf("agent should start on a forward, got %+v", a.Body)
	}
	b := w.AddAgent("a2", "B", 0)
	if b.Body == a.Body || b.Body.Team != 0 {
		t.Fatal("second agent must get a different teammate")
	}
}

func TestBotsPlayFootball(t *testing.T) {
	w := NewWorld(90, 11)
	w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	w.Start()
	moved, restarts := 0.0, 0
	prev := w.Ball.Pos
	for i := 0; i < 160*TickRate && w.Phase != PhaseFinished; i++ {
		w.Step()
		moved += w.Ball.Pos.Sub(prev).Len()
		prev = w.Ball.Pos
		for _, e := range w.DrainEvents() {
			if e.Type == "restart" {
				restarts++
			}
		}
		for _, p := range w.Players {
			if math.IsNaN(p.Pos.X) || math.Abs(p.Pos.X) > FieldHalfX || math.Abs(p.Pos.Z) > FieldHalfZ {
				t.Fatalf("player %s left the pitch: %v", p.ID, p.Pos)
			}
		}
		if math.IsNaN(w.Ball.Pos.X) {
			t.Fatal("ball NaN")
		}
	}
	if w.Phase != PhaseFinished {
		t.Fatal("match should finish")
	}
	if moved < 200 || restarts < 2 {
		t.Fatalf("bots should actually play: ball travelled %.0f, restarts %d, score %v", moved, restarts, w.Score)
	}
}

func TestKickScoresGoal(t *testing.T) {
	w, a, b := duel(t)
	b.Body.Pos = Vec{22, 20}
	a.Body.Pos = Vec{10, 0}
	w.Ball.Pos = Vec{a.Body.Pos.X + PlayerRadius + BallRadius + 0.5, 0}
	ok, err := w.Kick(a, Vec{1, 0}, 1, 0)
	if err != nil || !ok {
		t.Fatalf("kick ok=%v err=%v", ok, err)
	}
	for i := 0; i < 5*TickRate && w.Phase == PhasePlaying; i++ {
		w.Step()
	}
	if w.Score[0] != 1 {
		t.Fatalf("expected team 0 to score, score=%v ball=%v", w.Score, w.Ball.Pos)
	}
	for i := 0; i < int((GoalPauseSecs+RestartWait+0.5)*TickRate); i++ {
		w.Step()
	}
	if w.Phase != PhasePlaying || w.Ball.Pos.Len() > 1 {
		t.Fatalf("after a goal play restarts at the centre spot: phase=%s ball=%v", w.Phase, w.Ball.Pos)
	}
}

func TestSkillValidation(t *testing.T) {
	good := SkillSpec{Name: "Rocket", Effects: []Effect{{Type: "impulse_ball", Power: 30, Radius: 4}}, Visual: goodVisual()}
	sk, err := CompileSkill(good)
	if err != nil || sk.CreateCost <= 0 || sk.Cooldown <= 2 {
		t.Fatalf("good skill failed: %v %+v", err, sk)
	}
	cases := map[string]SkillSpec{
		"too strong":  {Name: "x", Effects: []Effect{{Type: "blast", Radius: 20, Power: 40, Targets: []string{"ball", "enemies", "allies"}}, {Type: "status", Status: "freeze", Target: "enemies", Duration: 5, Radius: 15}}, Visual: goodVisual()},
		"bad type":    {Name: "x", Effects: []Effect{{Type: "nuke"}}, Visual: goodVisual()},
		"out range":   {Name: "x", Effects: []Effect{{Type: "dash_self", Power: 99}}, Visual: goodVisual()},
		"banned code": {Name: "x", Effects: []Effect{{Type: "dash_self", Power: 5}}, Visual: Visual{Code: "ctx.group.add(1);fetch('http://evil')", DurationMs: 500}},
		"bracket":     {Name: "x", Effects: []Effect{{Type: "dash_self", Power: 5}}, Visual: Visual{Code: "ctx['gro'+'up']", DurationMs: 500}},
		"no visual":   {Name: "x", Effects: []Effect{{Type: "dash_self", Power: 5}}},
	}
	for name, spec := range cases {
		if _, err := CompileSkill(spec); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := CompileSkill(SkillSpec{Name: "x", Effects: []Effect{{Type: "dash_self", Power: 5}}, Visual: Visual{Code: "ctx.group.add(ctx.group.constructor)", DurationMs: 500}}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("constructor access must be rejected, got %v", err)
	}
}

func TestCreateSkillLagsPlayer(t *testing.T) {
	w, a, _ := duel(t)
	spec := SkillSpec{Name: "Boom", Effects: []Effect{{Type: "blast", Radius: 10, Power: 20, Targets: []string{"ball"}}}, Visual: goodVisual()}
	sk, err := w.CreateSkill(a, 0, spec)
	if err != nil {
		t.Fatal(err)
	}
	if a.CompileLeft <= 0 || sk.CreateCost <= 0 || a.Body.speedMult() > 0.4 {
		t.Fatal("player should lag while compiling")
	}
	if e := w.UseSkill(a, 0, Vec{1, 0}); e == nil || e.Code != "compiling" {
		t.Fatalf("cast while compiling must fail, got %v", e)
	}
	if _, e := w.CreateSkill(a, 1, spec); e == nil || e.Code != "compiling" {
		t.Fatalf("a second skill while compiling must wait: %v", e)
	}
}

func TestBlastPushesBallAndCooldown(t *testing.T) {
	w, a, _ := duel(t)
	sk, _ := w.CreateSkill(a, 0, SkillSpec{Name: "Boom", Effects: []Effect{{Type: "blast", Radius: 10, Power: 25, Targets: []string{"ball"}}}, Visual: goodVisual()})
	a.CompileLeft = 0
	w.Ball.Pos = a.Body.Pos.Add(Vec{5, 0})
	w.Ball.Vel = Vec{}
	if e := w.UseSkill(a, 0, Vec{1, 0}); e != nil {
		t.Fatal(e)
	}
	if w.Ball.Vel.X < 20 {
		t.Fatalf("ball not pushed: %v", w.Ball.Vel)
	}
	if e := w.UseSkill(a, 0, Vec{1, 0}); e == nil || e.Code != "cooldown" {
		t.Fatalf("expected cooldown, got %v", e)
	}
	if a.RecoilLeft <= 0 || sk.Recoil <= 0 {
		t.Fatal("skill must have recoil consequence")
	}
}

func TestFreezeProjectileOnBot(t *testing.T) {
	w := NewWorld(60, 5)
	a := w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	if _, e := w.CreateSkill(a, 0, SkillSpec{Name: "Ice", Effects: []Effect{{Type: "projectile", Speed: 40, Radius: 1.2, Lifetime: 2,
		OnHit: []Effect{{Type: "status", Status: "freeze", Target: "enemies", Duration: 2, Radius: 2}}}}, Visual: goodVisual()}); e != nil {
		t.Fatal(e)
	}
	a.CompileLeft = 0
	var victim *Player
	for _, p := range w.Players {
		if p.Team == 1 && p.Agent == nil && p.Role == RoleDEF {
			victim = p
		}
	}
	a.Body.Pos, victim.Pos = Vec{-20, 0}, Vec{-5, 0}
	for _, p := range w.Players { // clear the lane
		if p != a.Body && p != victim {
			p.Pos = Vec{p.Pos.X, 27 * sign(p.Pos.Z+0.1)}
		}
	}
	w.Ball.Pos = Vec{0, 25}
	if e := w.UseSkill(a, 0, victim.Pos); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < TickRate && victim.Frozen == 0; i++ {
		w.Step()
	}
	if victim.Frozen <= 0 {
		t.Fatal("target should be frozen")
	}
}

func TestWallBlocksBall(t *testing.T) {
	w, a, _ := duel(t)
	a.Body.Pos = Vec{-30, 0}
	w.Walls = append(w.Walls, &Wall{A: Vec{0, -8}, B: Vec{0, 8}, Left: 5})
	w.Ball.Pos, w.Ball.Vel = Vec{-5, 0}, Vec{30, 0}
	for i := 0; i < TickRate; i++ {
		w.Step()
	}
	if w.Ball.Pos.X > 0 {
		t.Fatalf("ball tunnelled through wall: %v", w.Ball.Pos)
	}
}

func TestSwitchPlayer(t *testing.T) {
	w := NewWorld(60, 11)
	w.AutoControl = false // this test exercises the manual switch button
	a := w.AddAgent("a1", "A", 0)
	b := w.AddAgent("a2", "B", 0)
	w.AddAgent("a3", "C", 1)
	startPlay(t, w)
	old := a.Body
	if _, e := w.SwitchPlayer(a, b.Body.ID); e == nil || e.Code != "occupied" {
		t.Fatalf("must not steal a player another agent controls: %v", e)
	}
	if _, e := w.SwitchPlayer(a, "t1p3"); e == nil || e.Code != "bad_player" {
		t.Fatalf("must not switch to the other team: %v", e)
	}
	np, e := w.SwitchPlayer(a, "t0p1") // goalkeeper
	if e != nil || np.Role != RoleGK || a.Body != np || np.Agent != a {
		t.Fatalf("switch failed: %v", e)
	}
	if old.Agent != nil {
		t.Fatal("the player left behind must go back to the bot AI")
	}
	if _, e := w.SwitchPlayer(a, ""); e == nil || e.Code != "cooldown" {
		t.Fatalf("switch cooldown missing: %v", e)
	}
	a.SwitchCD = 0
	w.Ball.Pos = Vec{-10, 5}
	np2, e := w.SwitchPlayer(a, "")
	if e != nil || np2.Agent != a || np2 == np || np2.Team != 0 {
		t.Fatalf("auto switch to nearest free teammate failed: %v %+v", e, np2)
	}
	if np.Agent != nil {
		t.Fatal("goalkeeper should be released to the bot")
	}
}

func TestThrowInGoalKickCorner(t *testing.T) {
	cases := []struct {
		name  string
		ball  Vec
		last  int
		kind  string
		team  int
		check func(*World) bool
	}{
		{"throw-in to opponents", Vec{10, 31}, 0, "throw_in", 1, func(w *World) bool { return math.Abs(w.Restart.Spot.Z) < FieldHalfZ }},
		{"goal kick for defenders", Vec{-51, 20}, 1, "goal_kick", 0, func(w *World) bool { return w.Restart.Spot.X > -FieldHalfX }},
		{"corner for attackers", Vec{-51, -20}, 0, "corner", 1, func(w *World) bool { return math.Abs(w.Restart.Spot.Z) > 25 && w.Restart.Spot.X < 0 }},
	}
	for _, c := range cases {
		w := NewWorld(60, 11)
		w.AddAgent("a1", "A", 0)
		w.AddAgent("a2", "B", 1)
		startPlay(t, w)
		w.Ball.Pos, w.Ball.Vel, w.LastTouchTeam = c.ball, Vec{}, c.last
		w.Step()
		r := w.Restart
		if r == nil || r.Kind != c.kind || r.Team != c.team || !c.check(w) {
			t.Fatalf("%s: got restart %+v", c.name, r)
		}
		for _, p := range w.Players { // opponents keep distance from the set piece
			if p.Team != r.Team && p.Pos.Sub(r.Spot).Len() < r.Keepout-0.5 {
				t.Fatalf("%s: opponent %s too close to the spot", c.name, p.ID)
			}
		}
		taken := false
		for i := 0; i < int((RestartWait+0.5)*TickRate); i++ {
			w.Step()
			for _, e := range w.DrainEvents() {
				if e.Type == "restart_taken" && e.Data["kind"] == c.kind {
					taken = true
				}
			}
		}
		if !taken {
			t.Fatalf("%s: a bot should have taken the set piece", c.name)
		}
	}
}

func TestBoundsAndMatchEnds(t *testing.T) {
	w := NewWorld(30, 7)
	w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	for i := 0; i < 40*TickRate && w.Phase != PhaseFinished; i++ {
		w.Step()
	}
	if w.Phase != PhaseFinished {
		t.Fatalf("match should end, phase=%s", w.Phase)
	}
}

func TestBallContactTransfersControl(t *testing.T) {
	w := NewWorld(60, 11)
	w.Autopilot = false
	a := w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	a.autoLock = 0
	old := a.Body
	var mate *Player
	for _, p := range w.Players {
		if p.Team == 0 && p.Agent == nil && p.Role == RoleMID && p != old {
			mate = p
			break
		}
	}
	// the ball rolls onto a free teammate far from the agent's own player
	old.Pos = Vec{-30, 25}
	mate.Pos = Vec{-10, -5}
	w.Ball.Pos, w.Ball.Vel = mate.Pos.Add(Vec{PlayerRadius + BallRadius - 0.2, 0}), Vec{}
	w.Step()
	if a.Body != mate || mate.Agent != a {
		t.Fatalf("the player who touches the ball must become the agent's player, controls %s", a.Body.ID)
	}
	if old.Agent != nil {
		t.Fatal("the previous player must go back to the bot AI")
	}
	// an opponent touching the ball must not pull control away
	var foe *Player
	for _, p := range w.Players {
		if p.Team == 1 && p.Role == RoleMID {
			foe = p
			break
		}
	}
	a.autoLock = 0
	foe.Pos = Vec{20, 5}
	w.Ball.Pos = foe.Pos.Add(Vec{-(PlayerRadius + BallRadius - 0.2), 0})
	w.Step()
	if a.Body != mate {
		t.Fatal("opponent contact must not change control")
	}
	// when the agent's own player is on the ball nobody else takes over
	a.autoLock = 0
	other := w.PlayerByID("t0p8")
	other.Pos = Vec{-9, 5}
	mate.Pos = Vec{-10, -5}
	w.Ball.Pos = mate.Pos.Add(Vec{PlayerRadius + BallRadius - 0.2, 0})
	other.Pos = w.Ball.Pos.Add(Vec{0, PlayerRadius + BallRadius - 0.2})
	w.Step()
	if a.Body != mate {
		t.Fatal("agent already on the ball must keep control")
	}
}

func TestLoftedBallFliesOverPlayers(t *testing.T) {
	w, a, b := duel(t)
	a.Body.Pos, b.Body.Pos = Vec{0, 0}, Vec{14, 0}
	w.Ball.Pos = Vec{3.8, 0}
	if ok, e := w.Kick(a, Vec{1, 0}, 0.75, 1); !ok || e != nil {
		t.Fatalf("lofted kick failed: %v %v", ok, e)
	}
	passed, maxY := false, 0.0
	for i := 0; i < 2*TickRate; i++ {
		w.Step()
		if w.Ball.Y > maxY {
			maxY = w.Ball.Y
		}
		if w.Ball.Pos.X > b.Body.Pos.X+3 {
			passed = true
			break
		}
	}
	if !passed || maxY < 3 {
		t.Fatalf("a lofted ball must fly over the opponent: passed=%v peak=%.1f ball=%v", passed, maxY, w.Ball.Pos)
	}
	// the same flat kick is blocked by the disc
	w2, a2, b2 := duel(t)
	a2.Body.Pos, b2.Body.Pos = Vec{0, 0}, Vec{14, 0}
	w2.Ball.Pos = Vec{3.8, 0}
	w2.Kick(a2, Vec{1, 0}, 0.75, 0)
	for i := 0; i < 2*TickRate; i++ {
		w2.Step()
	}
	if w2.Ball.Pos.X > b2.Body.Pos.X+3 {
		t.Fatalf("a ground ball must not pass through a disc: %v", w2.Ball.Pos)
	}
}

func TestBallBouncesAndSettles(t *testing.T) {
	w, _, _ := duel(t)
	w.Ball.Pos, w.Ball.Vel, w.Ball.Y, w.Ball.VY = Vec{-30, 25}, Vec{}, 6, 0
	bounced := false
	for i := 0; i < 4*TickRate; i++ {
		w.Step()
		if w.Ball.VY > 2 {
			bounced = true
		}
	}
	if !bounced || w.Ball.Y != 0 || w.Ball.VY != 0 {
		t.Fatalf("ball should bounce then settle: bounced=%v y=%.2f vy=%.2f", bounced, w.Ball.Y, w.Ball.VY)
	}
}

func TestShotOverTheBarIsNotAGoal(t *testing.T) {
	w, _, _ := duel(t)
	w.Ball.Pos, w.Ball.Vel, w.Ball.Y, w.Ball.VY = Vec{46, 0}, Vec{20, 0}, 5, 0
	w.LastTouchTeam = 0
	for i := 0; i < TickRate && w.Restart == nil; i++ {
		w.Step()
	}
	if w.Score[0] != 0 || w.Restart == nil || w.Restart.Kind != "goal_kick" {
		t.Fatalf("ball over the bar: want goal kick, score=%v restart=%+v", w.Score, w.Restart)
	}
}

func TestLobSkillAndLiftEffects(t *testing.T) {
	w := NewWorld(60, 1)
	w.Autopilot = false
	a := w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	spec := SkillSpec{Name: "Chip", Effects: []Effect{{Type: "lob_ball", Power: 18, Lift: 20, Radius: 6}}, Visual: goodVisual()}
	if _, e := w.CreateSkill(a, 0, spec); e != nil {
		t.Fatal(e)
	}
	a.CompileLeft = 0
	w.Ball.Pos = a.Body.Pos.Add(Vec{4, 0})
	if e := w.UseSkill(a, 0, Vec{40, 0}); e != nil {
		t.Fatal(e)
	}
	if w.Ball.VY < 19 || w.Ball.Vel.X < 17 {
		t.Fatalf("lob_ball must pop the ball up and forward: vel=%v vy=%.1f", w.Ball.Vel, w.Ball.VY)
	}
	for name, e := range map[string]Effect{
		"lift too high":     {Type: "lob_ball", Power: 10, Lift: 99, Radius: 5},
		"lift too low":      {Type: "lob_ball", Power: 10, Lift: 2, Radius: 5},
		"impulse lift high": {Type: "impulse_ball", Power: 10, Radius: 4, Lift: 80},
		"blast lift high":   {Type: "blast", Power: 10, Radius: 4, Lift: 60},
	} {
		if _, err := CompileSkill(SkillSpec{Name: "x", Effects: []Effect{e}, Visual: goodVisual()}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	geyser := SkillSpec{Name: "Geyser", Effects: []Effect{{Type: "blast", Radius: 8, Power: 4, Lift: 22, Targets: []string{"ball"}}}, Visual: goodVisual()}
	sk, err := CompileSkill(geyser)
	if err != nil || sk.Power <= 4*0.4+8*0.5 {
		t.Fatalf("lift must add to the power score: %v %+v", err, sk)
	}
}

func TestSkillUseIsLoggedWithOutcome(t *testing.T) {
	w := NewWorld(60, 5)
	w.Autopilot, w.AutoControl = false, false
	a := w.AddAgent("a1", "Ada", 0)
	w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	var victim *Player
	for _, p := range w.Players {
		if p.Team == 1 && p.Agent == nil && p.Role == RoleDEF {
			victim = p
		}
	}
	for _, p := range w.Players {
		if p != a.Body && p != victim {
			p.Pos = Vec{p.Pos.X, 27 * sign(p.Pos.Z+0.1)}
		}
	}
	a.Body.Pos, victim.Pos = Vec{-20, 0}, Vec{-8, 0}
	w.Ball.Pos = Vec{-14, 0}
	spec := SkillSpec{Name: "Frost Pulse", Effects: []Effect{
		{Type: "blast", Radius: 14, Power: 12, Lift: 15, Targets: []string{"ball", "enemies"}},
		{Type: "status", Status: "freeze", Target: "enemies", Duration: 2, Radius: 14}}, Visual: goodVisual()}
	if _, e := w.CreateSkill(a, 0, spec); e != nil {
		t.Fatal(e)
	}
	a.CompileLeft = 0
	w.DrainEvents()
	if e := w.UseSkill(a, 0, victim.Pos); e != nil {
		t.Fatal(e)
	}
	var result map[string]any
	for _, e := range w.DrainEvents() {
		if e.Type == "skill_result" {
			result = e.Data
		}
	}
	if result == nil || result["name"] != "Frost Pulse" {
		t.Fatalf("a cast must publish a skill_result: %v", result)
	}
	notes := result["notes"].([]string)
	joined := ""
	for _, n := range notes {
		joined += n + "; "
	}
	for _, want := range []string{"blasted the ball", "lift 15", "froze"} {
		if !strings.Contains(joined, want) {
			t.Errorf("log should mention %q, got %q", want, joined)
		}
	}
	if victim.Frozen <= 0 || w.Ball.VY < 14 {
		t.Fatalf("the effects must actually happen: frozen=%.1f vy=%.1f", victim.Frozen, w.Ball.VY)
	}
}
