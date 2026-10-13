package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentswars/internal/api"
	"agentswars/internal/mcp"
	"agentswars/internal/room"
)

type client struct {
	t    *testing.T
	url  string
	id   int
	code string
	host string
}

func (c *client) rpc(method string, params any) map[string]any {
	c.id++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	res, err := http.Post(c.url+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out
}

// tool returns (decoded payload, isError).
func (c *client) tool(name string, args map[string]any) (map[string]any, bool) {
	r := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	res := r["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var payload map[string]any
	_ = json.Unmarshal([]byte(text), &payload)
	isErr, _ := res["isError"].(bool)
	return payload, isErr
}

func (c *client) http(method, path string, body any, hdr map[string]string) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, c.url+path, bytes.NewReader(b))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func setup(t *testing.T) *client {
	srv := httptest.NewServer(api.Handler(room.NewManager(), ""))
	t.Cleanup(srv.Close)
	c := &client{t: t, url: srv.URL}
	_, info := c.http("POST", "/api/rooms", map[string]any{"players_per_team": 11, "agents_per_team": 1, "duration_secs": 30, "time_scale": 1}, nil)
	c.code, c.host = info["code"].(string), info["host_token"].(string)
	return c
}

func fireBrief() map[string]any {
	return map[string]any{
		"persona": map[string]any{"style": "aggressive", "preferred_role": "MID", "motto": "burn it down"},
		"skills": []any{map[string]any{"slot": 0, "concept": "a fire blast that launches the ball", "archetype": "attack",
			"element": "fire", "intensity": "low", "power_budget": 20}},
	}
}

func fireSpec(color string) map[string]any {
	return map[string]any{"name": "Ember Burst", "effects": []any{map[string]any{"type": "blast", "radius": 8, "power": 15, "targets": []any{"ball"}}},
		"visual": map[string]any{"color": color, "duration_ms": 900, "code": "const m=new THREE.Mesh(new THREE.SphereGeometry(1),new THREE.MeshBasicMaterial({color:ctx.color}));ctx.group.add(m);return {update(t){m.scale.setScalar(1+t)}};"}}
}

func TestProtocolRolesAndSquads(t *testing.T) {
	c := setup(t)
	init := c.rpc("initialize", map[string]any{})
	if init["result"].(map[string]any)["instructions"] == "" {
		t.Fatal("initialize must carry the protocol instructions")
	}
	have := map[string]bool{}
	for _, tl := range c.rpc("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		have[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"set_plan", "get_plan", "clear_plan", "pass_to", "launch", "forge_skill", "ready", "switch_player", "report_usage"} {
		if !have[want] {
			t.Fatalf("tool %s missing", want)
		}
	}
	if p, isErr := c.tool("join_room", map[string]any{"code": c.code, "name": "A", "rules_ack": "nope"}); !isErr || p["error"] != "rules_not_acknowledged" {
		t.Fatalf("want rules_not_acknowledged, got %v", p)
	}
	rules, _ := c.tool("get_rules", nil)
	ack := rules["rules_version"].(string)
	a, isErr := c.tool("join_room", map[string]any{"code": c.code, "name": "Alpha", "rules_ack": ack})
	if isErr {
		t.Fatal(a)
	}
	b, _ := c.tool("join_room", map[string]any{"code": c.code, "name": "Beta", "rules_ack": ack})
	if b["team"].(float64) != 1 {
		t.Fatalf("second agent should be on team 1: %v", b)
	}
	if _, isErr := c.tool("join_room", map[string]any{"code": c.code, "name": "Gamma", "rules_ack": ack}); !isErr {
		t.Fatal("room should be full")
	}
	cmd, eye, exe := a["commander_token"].(string), a["eye_token"].(string), a["executor_token"].(string)

	// wrong role => violation + strike + sin-bin
	if p, isErr := c.tool("move", map[string]any{"token": cmd, "dir_x": 1, "dir_z": 0}); !isErr || p["error"] != "role_violation" {
		t.Fatalf("commander moving must be a role violation: %v", p)
	}
	if p, isErr := c.tool("observe", map[string]any{"token": exe}); !isErr || p["error"] != "role_violation" {
		t.Fatalf("executor observing must be a role violation: %v", p)
	}
	if p, isErr := c.tool("switch_player", map[string]any{"token": eye, "player_id": "t0p1"}); !isErr || p["error"] != "role_violation" {
		t.Fatalf("eye switching must be a role violation: %v", p)
	}
	// planning is the commander's job, passing/launching the executor's
	if p, isErr := c.tool("set_plan", map[string]any{"token": exe}); !isErr || p["error"] != "role_violation" {
		t.Fatalf("executor planning must be a role violation: %v", p)
	}
	if p, isErr := c.tool("pass_to", map[string]any{"token": cmd, "player_id": "t0p7"}); !isErr || p["error"] != "role_violation" {
		t.Fatalf("commander passing must be a role violation: %v", p)
	}
	st, _ := c.tool("get_status", map[string]any{"token": cmd})
	if st["you"].(map[string]any)["strikes"].(float64) != 5 {
		t.Fatalf("strikes not recorded: %v", st["you"])
	}
	if p, isErr := c.tool("observe", map[string]any{"token": "garbage"}); !isErr || p["error"] != "bad_token" {
		t.Fatal("bad token must be rejected")
	}

	// full squads: 22 bodies, 10 bot teammates, no TPS limits on observing
	var obs map[string]any
	for i := 0; i < 50; i++ {
		if obs, isErr = c.tool("observe", map[string]any{"token": eye}); isErr {
			t.Fatalf("observe must never be throttled: %v", obs)
		}
	}
	players := obs["players"].([]any)
	if len(players) != 22 || obs["your_player"] == "" {
		t.Fatalf("want 22 players and a controlled body, got %d / %v", len(players), obs["your_player"])
	}
	bots := 0
	for _, p := range players {
		if pm := p.(map[string]any); pm["team"].(float64) == 0 && pm["agent"] == nil {
			bots++
		}
	}
	if bots != 10 {
		t.Fatalf("want 10 bot teammates, got %d", bots)
	}
	if p, isErr := c.tool("move", map[string]any{"token": exe, "dir_x": 1, "dir_z": 0}); !isErr || p["error"] != "not_playing" {
		t.Fatalf("expected not_playing before kickoff, got %v", p)
	}
	// live forging is not available before kickoff
	if p, isErr := c.tool("create_skill", map[string]any{"token": cmd, "slot": 0, "spec": fireSpec("#ff8030")}); !isErr || p["error"] != "prematch" {
		t.Fatalf("create_skill before kickoff must fail: %v", p)
	}
}

func TestTrainerBriefGuardrailsAndReady(t *testing.T) {
	c := setup(t)
	// only the host may edit briefs / start
	if code, _ := c.http("PUT", "/api/rooms/"+c.code+"/slots/0/0/brief", fireBrief(), nil); code != 403 {
		t.Fatalf("brief edit without host token must be 403, got %d", code)
	}
	if code, _ := c.http("POST", "/api/rooms/"+c.code+"/start", nil, nil); code != 403 {
		t.Fatalf("start without host token must be 403, got %d", code)
	}
	if code, out := c.http("PUT", "/api/rooms/"+c.code+"/slots/0/0/brief", map[string]any{"skills": []any{map[string]any{"slot": 0, "concept": "x y z", "archetype": "nuke", "element": "fire"}}}, map[string]string{"X-Host-Token": c.host}); code != 400 {
		t.Fatalf("invalid brief must be 400, got %d %v", code, out)
	}
	if code, out := c.http("PUT", "/api/rooms/"+c.code+"/slots/0/0/brief", fireBrief(), map[string]string{"X-Host-Token": c.host}); code != 200 {
		t.Fatalf("brief edit failed: %d %v", code, out)
	}

	rules, _ := c.tool("get_rules", nil)
	ack := rules["rules_version"]
	a, _ := c.tool("join_room", map[string]any{"code": c.code, "name": "Alpha", "rules_ack": ack, "provider": "Acme AI", "model": "turbo-1"})
	br := a["brief"].(map[string]any)
	if br["persona"].(map[string]any)["style"] != "aggressive" || len(br["skills"].([]any)) != 1 {
		t.Fatalf("agent must receive its trainer's brief: %v", br)
	}
	cmd := a["commander_token"].(string)
	b, _ := c.tool("join_room", map[string]any{"code": c.code, "name": "Beta", "rules_ack": ack})
	cmdB := b["commander_token"].(string)

	// preferred position honoured
	snap, _ := c.tool("get_status", map[string]any{"token": cmd})
	if snap["phase"] != "lobby" {
		t.Fatalf("phase %v", snap["phase"])
	}

	for name, tc := range map[string]struct {
		slot int
		spec map[string]any
		code string
	}{
		"unbriefed slot":  {1, fireSpec("#ff8030"), "no_brief"},
		"off-theme color": {0, fireSpec("#3060ff"), "guardrail"},
		"wrong effect":    {0, map[string]any{"name": "Wall", "effects": []any{map[string]any{"type": "wall", "length": 6, "duration": 1, "distance": 4}}, "visual": fireSpec("#ff8030")["visual"]}, "guardrail"},
	} {
		if p, isErr := c.tool("forge_skill", map[string]any{"token": cmd, "slot": tc.slot, "spec": tc.spec}); !isErr || p["error"] != tc.code {
			t.Errorf("%s: want %s, got %v", name, tc.code, p)
		}
	}
	if p, isErr := c.tool("forge_skill", map[string]any{"token": cmd, "slot": 0, "spec": fireSpec("#ff7a20")}); isErr {
		t.Fatalf("on-brief skill must be accepted: %v", p)
	}
	if p, _ := c.tool("get_brief", map[string]any{"token": cmd}); p["ready"] != false {
		t.Fatal("not ready yet")
	}

	// ready: match starts once both agents are ready
	c.tool("ready", map[string]any{"token": cmd})
	if st, _ := c.tool("get_status", map[string]any{"token": cmd}); st["phase"] != "lobby" {
		t.Fatalf("must wait for the other agent, phase=%v", st["phase"])
	}
	c.tool("ready", map[string]any{"token": cmdB})
	if st, _ := c.tool("get_status", map[string]any{"token": cmd}); st["phase"] != "countdown" {
		t.Fatalf("match should start when everybody is ready, phase=%v", st["phase"])
	}
	if p, isErr := c.tool("forge_skill", map[string]any{"token": cmd, "slot": 0, "spec": fireSpec("#ff7a20")}); !isErr || p["error"] != "not_prematch" {
		t.Fatalf("forge after kickoff must fail: %v", p)
	}
	if code, _ := c.http("PUT", "/api/rooms/"+c.code+"/slots/0/0/brief", fireBrief(), map[string]string{"X-Host-Token": c.host}); code != 400 {
		t.Fatalf("briefs are frozen after kickoff, got %d", code)
	}

	// benchmark: measured numbers + self-reported tokens/sec
	time.Sleep(20 * time.Millisecond)
	if p, isErr := c.tool("report_usage", map[string]any{"token": cmd, "input_tokens": 1200, "output_tokens": 600, "generation_ms": 3000}); isErr {
		t.Fatal(p)
	}
	_, bench := c.http("GET", "/api/rooms/"+c.code+"/benchmark", nil, nil)
	rows := bench["bench"].([]any)
	top := rows[0].(map[string]any)
	if top["name"] != "Alpha" || top["provider"] != "Acme AI" || top["tokens_per_sec"].(float64) != 200 || top["calls"].(float64) < 5 {
		t.Fatalf("benchmark row wrong: %v", top)
	}
	if mcp.RulesVersion == "" {
		t.Fatal("rules version")
	}
}
