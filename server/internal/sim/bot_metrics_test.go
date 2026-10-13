package sim

import (
	"fmt"
	"math"
	"testing"
)

// botMatch plays a bot-only 11v11 (two idle agents so autopilot drives their players too).
var lastStats map[string]int

func botMatch(seed int64, secs float64) (goals int, restarts map[string]int, clump float64, passes, kicks int) {
	w := NewWorld(secs, 11)
	defer func() {
		if lastStats == nil {
			lastStats = map[string]int{}
		}
		for k, v := range w.Stats {
			lastStats[k] += v
		}
	}()
	w.SetSeed(seed)
	w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	w.Start()
	restarts = map[string]int{}
	var n int
	var lastKicker *Player
	for i := 0; i < int(secs+30)*TickRate && w.Phase != PhaseFinished; i++ {
		w.Step()
		for _, e := range w.DrainEvents() {
			switch e.Type {
			case "goal":
				goals++
			case "restart":
				restarts[e.Data["kind"].(string)]++
			case "kick":
				kicks++
				p := w.PlayerByID(e.Data["player"].(string))
				if lastKicker != nil && p.Team == lastKicker.Team && p != lastKicker {
					passes++
				}
				lastKicker = p
			}
		}
		if w.Phase == PhasePlaying && i%15 == 0 {
			c := 0
			for _, p := range w.Players {
				if p.Pos.Sub(w.Ball.Pos).Len() < 9 {
					c++
				}
			}
			clump += float64(c)
			n++
		}
	}
	if n > 0 {
		clump /= float64(n)
	}
	return
}

func TestBotFootballMetrics(t *testing.T) {
	const matches = 8
	var goals, passes, kicks int
	var clump float64
	rs := map[string]int{}
	for s := int64(1); s <= matches; s++ {
		g, r, c, p, k := botMatch(s, 180)
		goals += g
		passes += p
		kicks += k
		clump += c
		for k, v := range r {
			rs[k] += v
		}
		if math.IsNaN(c) {
			t.Fatal("nan")
		}
	}
	fmt.Printf("METRICS goals/match=%.1f clump(players within 9 of ball)=%.1f passes/match=%.0f kicks/match=%.0f restarts/match=%v\n",
		float64(goals)/matches, clump/matches, float64(passes)/matches, float64(kicks)/matches,
		map[string]float64{"throw_in": float64(rs["throw_in"]) / matches, "corner": float64(rs["corner"]) / matches, "goal_kick": float64(rs["goal_kick"]) / matches, "kickoff": float64(rs["kickoff"]) / matches})
	fmt.Println("DECISIONS/match:", func() map[string]int {
		m := map[string]int{}
		for k, v := range lastStats {
			m[k] = v / matches
		}
		return m
	}())
	if goals == 0 {
		t.Fatal("bots never scored in 8 matches")
	}
}
