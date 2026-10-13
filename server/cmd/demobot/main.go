// demobot plays Agents Wars through the public MCP endpoint, exactly like an AI agent
// would: one commander, one eye goroutine and one executor goroutine per bot, each holding
// only its own token. Use it to demo the game and to test the stack end to end.
//
//	go run ./cmd/demobot -code ABC123 -agents 2
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	base      = flag.String("url", "http://localhost:8080", "server base url")
	numSkills = flag.Int("skills", 2, "skills each bot forges (1-3); more skills = less TPS for playing")
)

type rpc struct {
	id atomic.Int64
}

func (r *rpc) call(tool string, args map[string]any) (map[string]any, bool) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": r.id.Add(1), "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	res, err := http.Post(*base+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("mcp: %v", err)
	}
	defer res.Body.Close()
	var out struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	var payload map[string]any
	if len(out.Result.Content) > 0 {
		_ = json.Unmarshal([]byte(out.Result.Content[0].Text), &payload)
	}
	return payload, out.Result.IsError
}

type vec struct{ X, Z float64 }

type obs struct {
	Tick    int
	Phase   string
	Ball    vec
	Mine    string
	Players []struct {
		ID    string
		Team  int
		Pos   vec
		Role  string
		Agent string
	}
}

func parseObs(p map[string]any) *obs {
	b, _ := json.Marshal(p)
	var raw struct {
		Tick    int    `json:"tick"`
		Phase   string `json:"phase"`
		Mine    string `json:"your_player"`
		BallPos vec    `json:"ball_pos"`
		Players []struct {
			ID    string                 `json:"id"`
			Team  int                    `json:"team"`
			Role  string                 `json:"role"`
			Agent string                 `json:"agent"`
			Pos   struct{ X, Z float64 } `json:"pos"`
		} `json:"players"`
	}
	_ = json.Unmarshal(b, &raw)
	o := &obs{Tick: raw.Tick, Phase: raw.Phase, Ball: raw.BallPos, Mine: raw.Mine}
	for _, p := range raw.Players {
		o.Players = append(o.Players, struct {
			ID    string
			Team  int
			Pos   vec
			Role  string
			Agent string
		}{p.ID, p.Team, vec{p.Pos.X, p.Pos.Z}, p.Role, p.Agent})
	}
	return o
}

func norm(a vec) vec {
	l := math.Hypot(a.X, a.Z)
	if l < 1e-9 {
		return vec{}
	}
	return vec{a.X / l, a.Z / l}
}

// One template per archetype. The bot recolours them to the trainer's element and clamps the visual
// to the brief's guardrails, exactly what an LLM agent is expected to do.
var templates = map[string]map[string]any{
	"attack": {"name": "Shockwave", "effects": []any{map[string]any{"type": "blast", "radius": 12, "power": 20, "lift": 12, "targets": []any{"ball", "enemies"}}},
		"visual": map[string]any{"duration_ms": 900, "code": `
const ring = new THREE.Mesh(new THREE.TorusGeometry(1, 0.18, 8, 48), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true}));
ring.rotation.x = Math.PI / 2; ring.position.y = 0.4; ctx.group.add(ring);
const pts = [];
for (let i = 0; i < 40; i++) {
  const m = new THREE.Mesh(new THREE.SphereGeometry(0.22, 6, 6), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true}));
  const a = ctx.rand() * Math.PI * 2; m.userData.a = a; m.userData.s = 0.5 + ctx.rand();
  ctx.group.add(m); pts.push(m);
}
return { update(t) {
  const r = 1 + t * ctx.reach;
  ring.scale.set(r, r, 1); ring.material.opacity = 1 - t;
  for (const m of pts) { const d = r * m.userData.s; m.position.set(Math.cos(m.userData.a) * d, 0.4 + t * 3 * m.userData.s, Math.sin(m.userData.a) * d); m.material.opacity = 1 - t; }
}};`}},
	"control": {"name": "Frost Bolt",
		"effects": []any{map[string]any{"type": "projectile", "speed": 40, "radius": 1.2, "lifetime": 1.5,
			"on_hit": []any{map[string]any{"type": "status", "status": "freeze", "target": "enemies", "duration": 1.5, "radius": 3}}}},
		"visual": map[string]any{"duration_ms": 1500, "code": `
const n = 14, bits = [];
for (let i = 0; i < n; i++) {
  const m = new THREE.Mesh(new THREE.OctahedronGeometry(0.5 - i * 0.025), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true}));
  ctx.group.add(m); bits.push(m);
}
return { update(t) {
  const head = Math.min(1, t / 0.9) * ctx.reach;
  bits.forEach((m, i) => {
    const d = Math.max(0, head - i * 1.1);
    m.position.set(ctx.aim.x * d, 1 + Math.sin(t * 20 + i) * 0.25, ctx.aim.z * d);
    m.rotation.y = t * 9 + i; m.material.opacity = t > 0.9 ? (1 - t) * 10 : 1;
  });
}};`}},
	"trick": {"name": "Magnet Well", "effects": []any{map[string]any{"type": "magnet", "radius": 14, "strength": 20, "duration": 2.5}},
		"visual": map[string]any{"duration_ms": 1200, "code": `
const rings = [];
for (let i = 0; i < 3; i++) {
  const m = new THREE.Mesh(new THREE.TorusGeometry(1, 0.1, 6, 40), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true}));
  m.rotation.x = Math.PI / 2; m.position.y = 0.3; ctx.group.add(m); rings.push(m);
}
return { update(t) {
  rings.forEach((m, i) => { const p = ((t * 3 + i / 3) % 1); const s = ctx.reach * (1 - p) + 1; m.scale.set(s, s, 1); m.material.opacity = p * (1 - t); });
}};`}},
	"defense": {"name": "Bulwark", "effects": []any{
		map[string]any{"type": "wall", "length": 8, "duration": 2.5, "distance": 5},
		map[string]any{"type": "status", "status": "shield", "target": "self", "duration": 2}},
		"visual": map[string]any{"duration_ms": 1000, "code": `
const slab = new THREE.Mesh(new THREE.BoxGeometry(8, 3, 0.6), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true, opacity: 0.6}));
slab.position.set(ctx.aim.x * 5, 1.5, ctx.aim.z * 5); slab.rotation.y = -Math.atan2(ctx.aim.z, ctx.aim.x) + Math.PI / 2;
ctx.group.add(slab);
return { update(t) { slab.scale.y = Math.min(1, t * 5); slab.material.opacity = 0.6 * (1 - t * t); }};`}},
	"mobility": {"name": "Afterburner", "effects": []any{
		map[string]any{"type": "dash_self", "power": 20},
		map[string]any{"type": "status", "status": "haste", "target": "self", "duration": 2}},
		"visual": map[string]any{"duration_ms": 900, "code": `
const trail = [];
for (let i = 0; i < 12; i++) {
  const m = new THREE.Mesh(new THREE.SphereGeometry(0.7 - i * 0.04, 8, 8), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true}));
  ctx.group.add(m); trail.push(m);
}
return { update(t) { trail.forEach((m, i) => { const d = (t * 12 + i) * 1.1; m.position.set(ctx.aim.x * d, 0.8, ctx.aim.z * d); m.material.opacity = 1 - t; }); }};`}},
}

func runBot(code, name string, wg *sync.WaitGroup) {
	defer wg.Done()
	var c rpc
	rules, _ := c.call("get_rules", nil)
	join, isErr := c.call("join_room", map[string]any{"code": code, "name": name, "rules_ack": rules["rules_version"], "provider": "demobot", "model": "scripted-1"})
	if isErr {
		log.Printf("[%s] join failed: %v", name, join)
		return
	}
	team := int(join["team"].(float64))
	cmd, eyeTok, exeTok := join["commander_token"].(string), join["eye_token"].(string), join["executor_token"].(string)
	me := join["agent_id"].(string)
	ax := 1.0
	if team == 1 {
		ax = -1
	}
	log.Printf("[%s] joined %s as %s team %d", name, code, me, team)

	var latest atomic.Pointer[obs]
	var kinds sync.Map // slot -> archetype of the skills this bot owns
	stop := make(chan struct{})

	// EYE subagent: only ever holds eye_token.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			p, isErr := c.call("observe", map[string]any{"token": eyeTok})
			if isErr {
				time.Sleep(time.Duration(num(p["retry_after"])*1000) * time.Millisecond)
				continue
			}
			latest.Store(parseObs(p))
			time.Sleep(350 * time.Millisecond)
		}
	}()

	// EXECUTOR subagent: only ever holds executor_token.
	go func() {
		step := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			o := latest.Load()
			if o == nil || o.Phase != "playing" {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			var mine vec
			var foe *vec
			foeD := math.MaxFloat64
			for _, p := range o.Players {
				if p.ID == o.Mine {
					mine = p.Pos
				}
			}
			for _, p := range o.Players {
				if p.Team != team {
					if d := math.Hypot(p.Pos.X-mine.X, p.Pos.Z-mine.Z); d < foeD {
						pp := p.Pos
						foe, foeD = &pp, d
					}
				}
			}
			goal := vec{ax * 50, 0}
			u := norm(vec{goal.X - o.Ball.X, goal.Z - o.Ball.Z})
			toBall := math.Hypot(o.Ball.X-mine.X, o.Ball.Z-mine.Z)
			args := func(m map[string]any) map[string]any { m["token"], m["based_on_tick"] = exeTok, o.Tick; return m }
			var p map[string]any
			var isErr bool
			if toBall > 20 && step%6 == 5 { // like pressing the switch button: take the free teammate nearest the ball
				if sw, bad := c.call("switch_player", args(map[string]any{})); !bad {
					log.Printf("[%s] switched to %v (%v)", name, sw["now_controlling"], sw["role"])
				}
			}
			switch {
			case toBall < 4.6:
				var mate *struct {
					ID    string
					Team  int
					Pos   vec
					Role  string
					Agent string
				}
				for i := range o.Players { // an open team-mate ahead of the ball: play the precise pass
					m := &o.Players[i]
					if m.Team != team || m.ID == o.Mine || m.Role == "GK" {
						continue
					}
					ahead := (m.Pos.X - o.Ball.X) * ax
					if d := math.Hypot(m.Pos.X-o.Ball.X, m.Pos.Z-o.Ball.Z); ahead > 6 && d > 8 && d < 30 && (mate == nil || ahead > (mate.Pos.X-o.Ball.X)*ax) {
						mate = m
					}
				}
				if mate != nil && math.Abs(goal.X-o.Ball.X) > 30 {
					p, isErr = c.call("pass_to", args(map[string]any{"player_id": mate.ID}))
					if !isErr {
						break
					}
				}
				p, isErr = c.call("kick", args(map[string]any{"dir_x": u.X, "dir_z": u.Z, "power": 1}))
			default:
				tx, tz := o.Ball.X-u.X*4, o.Ball.Z-u.Z*4+math.Sin(float64(step)*1.7+float64(team)*3)*5 // wander so two bots don't deadlock head-on
				d := norm(vec{tx - mine.X, tz - mine.Z})
				p, isErr = c.call("move", args(map[string]any{"dir_x": d.X, "dir_z": d.Z}))
				if !isErr {
					kinds.Range(func(k, v any) bool {
						sl, arch := k.(int), v.(string)
						switch {
						case arch == "control" && foe != nil && foeD < 22 && step%11 == 0:
							c.call("use_skill", args(map[string]any{"slot": sl, "aim_x": foe.X, "aim_z": foe.Z}))
						case arch == "attack" && toBall < 10 && step%13 == 0:
							c.call("use_skill", args(map[string]any{"slot": sl, "aim_x": goal.X, "aim_z": goal.Z}))
						case (arch == "trick" || arch == "mobility") && toBall > 14 && step%17 == 0:
							c.call("use_skill", args(map[string]any{"slot": sl, "aim_x": o.Ball.X, "aim_z": o.Ball.Z}))
						case arch == "defense" && foe != nil && foeD < 10 && step%19 == 0:
							c.call("use_skill", args(map[string]any{"slot": sl, "aim_x": foe.X, "aim_z": foe.Z}))
						}
						return true
					})
				}
			}
			step++
			wait := 300 * time.Millisecond
			if isErr {
				wait = time.Duration(num(p["retry_after"])*1000)*time.Millisecond/2 + 120*time.Millisecond
			}
			time.Sleep(wait)
		}
	}()

	// COMMANDER: reads the trainer's brief, forges briefed skills (pre-match), reports ready, then talks.
	for _, raw := range briefSkills(join) {
		sb := raw.(map[string]any)
		slot, arch := int(sb["slot"].(float64)), sb["archetype"].(string)
		tpl := cloneSpec(templates[arch])
		v := tpl["visual"].(map[string]any)
		v["color"] = sb["color"]
		if gr, ok := sb["guardrails"].(map[string]any); ok { // respect the guardrails instead of learning them by rejection
			if ms, ok := gr["max_visual_ms"].(float64); ok && v["duration_ms"].(int) > int(ms) {
				v["duration_ms"] = int(ms)
			}
		}
		p, isErr := c.call("forge_skill", map[string]any{"token": cmd, "slot": slot, "spec": tpl})
		if isErr {
			log.Printf("[%s] forge slot %d rejected: %v", name, slot, p["message"])
			continue
		}
		kinds.Store(slot, arch)
		log.Printf("[%s] forged %q for slot %d (%s, %s) power=%.0f", name, tpl["name"], slot, arch, sb["element"], num(p["power"]))
	}
	c.call("ready", map[string]any{"token": cmd})
	live := len(briefSkills(join)) == 0 // no trainer brief: forge live once the match is on
	lines := []string{"Pressing high!", "Watch this one.", "Ice incoming.", "Pulling the ball in.", "Nobody stops me."}
	for i := 0; ; i++ {
		time.Sleep(4 * time.Second)
		st, _ := c.call("get_status", map[string]any{"token": cmd})
		ph, _ := st["phase"].(string)
		if live && ph == "playing" {
			live = false
			if p, isErr := c.call("create_skill", map[string]any{"token": cmd, "slot": 0, "spec": templates["attack"]}); !isErr {
				kinds.Store(0, "attack")
				log.Printf("[%s] live-forged Shockwave, compile lag %.1fs", name, num(p["compile_secs"]))
			} else {
				log.Printf("[%s] live forge refused: %v", name, p["message"])
			}
		}
		if ph == "playing" && i%2 == 1 {
			if o := latest.Load(); o != nil { // plan: send the two nearest bot forwards on timed runs into space
				orders := []any{}
				for _, m := range o.Players {
					if m.Team == team && m.Agent == "" && (m.Role == "FWD" || m.Role == "MID") && len(orders) < 3 {
						side := float64(len(orders)-1) * 11
						orders = append(orders, map[string]any{"player_id": m.ID, "action": "move", "x": o.Ball.X + ax*14, "z": side, "arrive_s": 2.5, "ttl": 5})
					}
				}
				p, isErr := c.call("set_plan", map[string]any{"token": cmd, "orders": orders, "tactics": map[string]any{"line": "high", "width": "wide", "press": true}})
				if isErr {
					log.Printf("[%s] plan rejected: %v", name, p["message"])
				}
			}
		}
		if ph == "playing" {
			c.call("announce", map[string]any{"token": cmd, "text": fmt.Sprintf("%s: %s", name, lines[i%len(lines)])})
		}
		if ph == "finished" {
			log.Printf("[%s] match finished, score=%v", name, st["score"])
			close(stop)
			return
		}
	}
}

func briefSkills(join map[string]any) []any {
	if b, ok := join["brief"].(map[string]any); ok {
		if s, ok := b["skills"].([]any); ok {
			return s
		}
	}
	return nil
}

func cloneSpec(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	// json round-trips numbers as float64; duration_ms is compared as int below
	out["visual"].(map[string]any)["duration_ms"] = int(out["visual"].(map[string]any)["duration_ms"].(float64))
	return out
}

func num(v any) float64 { f, _ := v.(float64); return f }

func main() {
	code := flag.String("code", "", "room invite code")
	n := flag.Int("agents", 1, "how many bots to launch")
	flag.Parse()
	if *code == "" {
		log.Fatal("-code is required")
	}
	names := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot"}
	var wg sync.WaitGroup
	for i := 0; i < *n && i < len(names); i++ {
		wg.Add(1)
		go runBot(strings.ToUpper(*code), names[i], &wg)
		time.Sleep(200 * time.Millisecond)
	}
	wg.Wait()
}
