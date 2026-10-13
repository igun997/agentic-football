package sim

import (
	"math"
	"testing"
)

// still freezes the world's bots' own thinking by giving everyone but the actors a hold order.
func squad(t *testing.T) (*World, *Agent) {
	t.Helper()
	w := NewWorld(120, 11)
	w.Autopilot, w.AutoControl = false, false
	a := w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	startPlay(t, w)
	return w, a
}

func TestPlanArrivesExactlyOnTime(t *testing.T) {
	w, a := squad(t)
	mate := w.PlayerByID("t0p7")
	w.Ball.Pos = Vec{40, 25} // far away from everything
	mate.Pos, mate.Vel = Vec{-20, 0}, Vec{}
	target := Vec{-8, 6}
	res, e := w.SetPlan(a, []OrderSpec{{PlayerID: mate.ID, Action: "move", X: target.X, Z: target.Z, ArriveS: 2, TTL: 4}}, nil, true)
	if e != nil || len(res) != 1 || !res[0].Feasible || math.Abs(res[0].ETA-2) > 0.01 {
		t.Fatalf("plan result: %v %+v", e, res)
	}
	arrived := -1.0
	for i := 0; i < int(3.5*TickRate); i++ {
		w.Step()
		if arrived < 0 && mate.Pos.Sub(target).Len() < 1.5 {
			arrived = float64(i) * Dt
		}
	}
	if arrived < 1.5 || arrived > 2.5 {
		t.Fatalf("promised to arrive at 2.0s, got there at %.2fs (pos %v)", arrived, mate.Pos)
	}
	if mate.Pos.Sub(target).Len() > 2 {
		t.Fatalf("must hold the spot: %v", mate.Pos)
	}
}

func TestPlanReportsInfeasibleAndValidates(t *testing.T) {
	w, a := squad(t)
	mate := w.PlayerByID("t0p7")
	res, e := w.SetPlan(a, []OrderSpec{{PlayerID: mate.ID, Action: "move", X: 40, Z: 0, ArriveS: 0.8}}, nil, true)
	if e != nil || res[0].Feasible || res[0].Note == "" {
		t.Fatalf("an impossible arrival must be reported, not hidden: %v %+v", e, res)
	}
	bad := map[string][]OrderSpec{
		"own body":       {{PlayerID: a.Body.ID, Action: "hold"}},
		"other team":     {{PlayerID: "t1p4", Action: "hold"}},
		"unknown action": {{PlayerID: mate.ID, Action: "teleport"}},
		"bad ttl":        {{PlayerID: mate.ID, Action: "hold", TTL: 99}},
		"duplicate":      {{PlayerID: mate.ID, Action: "hold"}, {PlayerID: mate.ID, Action: "hold"}},
		"mark a mate":    {{PlayerID: mate.ID, Action: "mark", TargetID: "t0p8"}},
	}
	for name, specs := range bad {
		if _, e := w.SetPlan(a, specs, nil, true); e == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, e := w.SetPlan(a, nil, &Tactics{Line: "diagonal"}, true); e == nil || e.Code != "bad_tactics" {
		t.Fatalf("bad tactics accepted: %v", e)
	}
}

func TestTacticsReshapeTheTeam(t *testing.T) {
	line := func(tac Tactics) float64 {
		w := NewWorld(120, 11)
		w.Autopilot, w.AutoControl = false, false
		a := w.AddAgent("a1", "A", 0)
		w.AddAgent("a2", "B", 1)
		startPlay(t, w)
		w.SetPlan(a, nil, &tac, true)
		w.Ball.Pos = Vec{0, 0}
		for i := 0; i < 6*TickRate; i++ {
			w.Ball.Pos, w.Ball.Vel = Vec{0, 0}, Vec{}
			w.Step()
		}
		sum := 0.0
		for _, p := range w.Players {
			if p.Team == 0 && p.Role == RoleDEF {
				sum += p.Pos.X
			}
		}
		return sum / 4
	}
	high, low := line(Tactics{Line: "high"}), line(Tactics{Line: "low"})
	if high < low+6 {
		t.Fatalf("a high line must sit well upfield of a low block: high=%.1f low=%.1f", high, low)
	}
}

func TestLaunchStopsOnThePoint(t *testing.T) {
	w, a := squad(t)
	mate := w.PlayerByID("t0p8")
	w.Ball.Pos = Vec{40, 25}
	mate.Pos, mate.Vel = Vec{-20, -10}, Vec{}
	target := Vec{-12, -10}
	res, e := w.Launch(a, mate.ID, target)
	if e != nil || res.Clamped || math.Abs(res.Stop.X-target.X) > 0.01 {
		t.Fatalf("launch: %v %+v", e, res)
	}
	for i := 0; i < 3*TickRate; i++ {
		w.Step()
	}
	if d := mate.Pos.Sub(target).Len(); d > 1.2 {
		t.Fatalf("a flicked disc must coast to a stop on the point: %.2f away at %v", d, mate.Pos)
	}
	if _, e := w.Launch(a, mate.ID, Vec{0, 0}); e == nil || e.Code != "cooldown" {
		t.Fatalf("launch cooldown missing: %v", e)
	}
	far, _ := w.Launch(a, "t0p9", Vec{40, 0})
	if far != nil && !far.Clamped {
		t.Fatal("a flick beyond reach must be clamped and reported")
	}
}

func TestPassArrivesWhereAndWhenPromised(t *testing.T) {
	for _, style := range []string{"ground", "lob"} {
		w, a := squad(t)
		for _, p := range w.Players { // clear the lane
			if p != a.Body && p.ID != "t0p7" {
				p.Pos = Vec{p.Pos.X, 27 * sign(p.Pos.Z+0.1)}
				p.Frozen = 99
			}
		}
		mate := w.PlayerByID("t0p7")
		a.Body.Pos = Vec{-10, 0}
		w.Ball.Pos, w.Ball.Vel = Vec{-6, 0}, Vec{}
		mate.Pos, mate.Vel = Vec{12, 8}, Vec{}
		res, e := w.PassTo(a, mate.ID, nil, style, 0, true)
		if e != nil {
			t.Fatalf("%s: %v", style, e)
		}
		if res.Style != style || res.ETA <= 0.3 {
			t.Fatalf("%s: %+v", style, res)
		}
		var closest float64 = 1e9
		var atETA Vec
		for i := 0; i < int(res.ETA*TickRate)+TickRate; i++ {
			w.Step()
			if d := w.Ball.Pos.Sub(res.Aim).Len(); d < closest {
				closest = d
			}
			if i == int(res.ETA*TickRate) {
				atETA = w.Ball.Pos
			}
		}
		if closest > 3 {
			t.Fatalf("%s pass must reach the aim point: closest %.1f (ball %v aim %v)", style, closest, w.Ball.Pos, res.Aim)
		}
		if d := atETA.Sub(res.Aim).Len(); d > 6 {
			t.Fatalf("%s pass must arrive at the promised ETA: %.1f away at %.1fs", style, d, res.ETA)
		}
		if mate.Pos.Sub(res.Aim).Len() > 5 {
			t.Fatalf("%s: the receiver must be sent to the spot: mate %v aim %v", style, mate.Pos, res.Aim)
		}
	}
}

func TestPassErrors(t *testing.T) {
	w, a := squad(t)
	a.Body.Pos = Vec{-10, 0}
	w.Ball.Pos = Vec{30, 20}
	if _, e := w.PassTo(a, "t0p7", nil, "auto", 0, true); e == nil || e.Code != "no_ball" {
		t.Fatalf("pass without the ball: %v", e)
	}
	w.Ball.Pos = Vec{-6, 0}
	if _, e := w.PassTo(a, "t1p3", nil, "auto", 0, true); e == nil || e.Code != "bad_player" {
		t.Fatalf("pass to an opponent: %v", e)
	}
	if _, e := w.PassTo(a, "", &Vec{48, 28}, "ground", 0, true); e == nil || e.Code != "too_far" {
		t.Fatalf("a ground pass across the pitch must be refused: %v", e)
	}
}
