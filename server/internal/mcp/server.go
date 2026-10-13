// Package mcp exposes the game to AI agents as a Model Context Protocol server
// (JSON-RPC 2.0 over streamable HTTP, tools only).
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"agentswars/internal/room"
	"agentswars/internal/sim"
)

type Server struct {
	M     *room.Manager
	tools []tool
	index map[string]*tool
}

type tool struct {
	Name        string
	Description string
	Role        room.Role // "" = public, otherwise token must carry this role
	Schema      map[string]any
	Run         func(c *call) (any, error)
}

type call struct {
	auth room.Auth
	args map[string]any
	s    *Server
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(m *room.Manager) *Server {
	s := &Server{M: m, index: map[string]*tool{}}
	s.register()
	for i := range s.tools {
		s.index[s.tools[i].Name] = &s.tools[i]
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "MCP endpoint: POST JSON-RPC here", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var req rpcReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, nil, nil, &rpcErr{-32700, "parse error"})
		return
	}
	if len(req.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	res, rerr := s.dispatch(&req)
	writeRPC(w, req.ID, res, rerr)
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, res any, e *rpcErr) {
	out := map[string]any{"jsonrpc": "2.0"}
	if id != nil {
		out["id"] = json.RawMessage(id)
	} else {
		out["id"] = nil
	}
	if e != nil {
		out["error"] = e
	} else {
		out["result"] = res
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) dispatch(req *rpcReq) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agents-wars", "version": "0.1.0"},
			"instructions":    Instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			desc := t.Description
			if t.Role != "" {
				desc = fmt.Sprintf("[%s only] %s", t.Role, desc)
			}
			list = append(list, map[string]any{"name": t.Name, "description": desc, "inputSchema": t.Schema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		t := s.index[p.Name]
		if t == nil {
			return nil, &rpcErr{-32602, "unknown tool " + p.Name}
		}
		return s.callTool(t, p.Arguments), nil
	}
	return nil, &rpcErr{-32601, "method not found: " + req.Method}
}

// toolErr is a rule rejection returned to the agent as an isError tool result.
type toolErr struct {
	Code, Message string
	RetryAfter    float64
}

func (e *toolErr) Error() string { return e.Code + ": " + e.Message }

func (s *Server) callTool(t *tool, args map[string]any) map[string]any {
	start := time.Now()
	if args == nil {
		args = map[string]any{}
	}
	c := &call{args: args, s: s}
	if t.Role != "" || args["token"] != nil {
		tok, _ := args["token"].(string)
		a, ok := s.M.Auth(tok)
		if !ok {
			return result(nil, &toolErr{Code: "bad_token", Message: "unknown or missing token"})
		}
		c.auth = a
		if t.Role != "" && a.Role != t.Role {
			a.Room.With(func(w *sim.World) {
				if ag := w.AgentByID(a.Seat.AgentID); ag != nil {
					w.Strike(ag, fmt.Sprintf("%s token used on %s tool %s", a.Role, t.Role, t.Name))
				}
			})
			a.Room.RecordCall(a.Seat.AgentID, a.Role, t.Name, start, time.Now(), true)
			return result(nil, &toolErr{Code: "role_violation", Message: fmt.Sprintf("tool %s needs the %s token but you sent a %s token. Strike recorded and your player is sin-binned (slowed 3s). Give each token only to its own subagent.", t.Name, t.Role, a.Role)})
		}
	}
	out, err := t.Run(c)
	if c.auth.Room != nil {
		c.auth.Room.RecordCall(c.auth.Seat.AgentID, c.auth.Role, t.Name, start, time.Now(), err != nil)
	}
	return result(out, err)
}

func result(out any, err error) map[string]any {
	if err != nil {
		var te *toolErr
		var ge *sim.GameError
		payload := map[string]any{}
		switch {
		case errors.As(err, &te):
			payload["error"] = te.Code
			payload["message"] = te.Message
			if te.RetryAfter > 0 {
				payload["retry_after"] = te.RetryAfter
			}
		case errors.As(err, &ge):
			payload["error"] = ge.Code
			payload["message"] = ge.Message
			if ge.RetryAfter > 0 {
				payload["retry_after"] = ge.RetryAfter
			}
		default:
			payload["error"] = "error"
			payload["message"] = err.Error()
		}
		b, _ := json.Marshal(payload)
		return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": string(b)}}}
	}
	b, _ := json.Marshal(out)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
}

// ---- argument helpers -------------------------------------------------------

func (c *call) str(k string) string  { s, _ := c.args[k].(string); return s }
func (c *call) num(k string) float64 { f, _ := c.args[k].(float64); return f }
func (c *call) integer(k string) int { return int(c.num(k)) }

func schema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

var (
	tokenProp = map[string]any{"type": "string", "description": "role token from join_room"}
	numProp   = func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	basedOn   = numProp("tick of the observation this command is based on (stale >90 ticks costs double)")
)

func (c *call) withAgent(fn func(w *sim.World, a *sim.Agent) error) error {
	var err error
	c.auth.Room.With(func(w *sim.World) {
		a := w.AgentByID(c.auth.Seat.AgentID)
		if a == nil {
			err = &toolErr{Code: "no_agent", Message: "agent missing"}
			return
		}
		err = fn(w, a)
	})
	return err
}

func asErr(e *sim.GameError) error {
	if e == nil {
		return nil
	}
	return e
}

func (s *Server) register() {
	dirProps := map[string]any{"token": tokenProp, "dir_x": numProp("direction x"), "dir_z": numProp("direction z")}
	specProp := map[string]any{"type": "object", "description": "see skill_guide; must also satisfy your trainer's brief for that slot (get_brief)"}
	s.tools = []tool{
		{Name: "get_rules", Description: "Read the full rules and the role protocol. Required before join_room (its rules_version is your rules_ack).",
			Schema: schema(nil, map[string]any{}),
			Run: func(c *call) (any, error) {
				return map[string]any{"rules_version": RulesVersion, "rules": RulesText, "protocol": Instructions}, nil
			}},
		{Name: "skill_guide", Description: "Reference for forge_skill / create_skill: the gameplay DSL with hard limits and the Three.js visual API.",
			Schema: schema(nil, map[string]any{}),
			Run:    func(c *call) (any, error) { return map[string]any{"guide": SkillGuide}, nil }},
		{Name: "join_room", Description: "Join a room with its invite code. Returns commander/eye/executor tokens and your trainer's brief. Spawn parallel EYE and EXECUTOR subagents and hand each only its own token.",
			Schema: schema([]string{"code", "name", "rules_ack"}, map[string]any{
				"code":      map[string]any{"type": "string", "description": "invite code from the human host"},
				"name":      map[string]any{"type": "string", "description": "your agent name (<=24 chars)"},
				"rules_ack": map[string]any{"type": "string", "description": "rules_version from get_rules"},
				"provider":  map[string]any{"type": "string", "description": "optional: AI provider (shown in the benchmark)"},
				"model":     map[string]any{"type": "string", "description": "optional: model name (shown in the benchmark)"}}),
			Run: func(c *call) (any, error) {
				if c.str("rules_ack") != RulesVersion {
					return nil, &toolErr{Code: "rules_not_acknowledged", Message: "call get_rules and pass its rules_version as rules_ack"}
				}
				r, seat, brief, err := c.s.M.Join(c.str("code"), c.str("name"), c.str("provider"), c.str("model"))
				if err != nil {
					return nil, &toolErr{Code: "join_failed", Message: err.Error()}
				}
				now := time.Now()
				r.RecordCall(seat.AgentID, room.RoleCommander, "join_room", now, now, false) // starts the commander's turnaround clock
				dir := "+x (attack the goal at x=+50, defend x=-50)"
				if seat.Team == 1 {
					dir = "-x (attack the goal at x=-50, defend x=+50)"
				}
				return map[string]any{
					"room": r.Code, "agent_id": seat.AgentID, "team": seat.Team, "attack_direction": dir,
					"controls":        r.Observe(seat.AgentID).YourPlayer,
					"commander_token": seat.Tokens[room.RoleCommander],
					"eye_token":       seat.Tokens[room.RoleEye],
					"executor_token":  seat.Tokens[room.RoleExecutor],
					"brief":           briefView(brief, nil),
					"next":            "1) Spawn two parallel subagents: EYE (observe loop, eye_token) and EXECUTOR (move/kick/dash/use_skill/switch_player, executor_token). 2) You are the COMMANDER: read your brief, forge each briefed skill with forge_skill (free, before kickoff), then call ready. The match starts when every agent is ready (or after a 90s prep window) or when the host presses start.",
				}, nil
			}},
		{Name: "get_brief", Description: "Your trainer's setup for you: persona, preferred position, and one brief per skill slot (concept, archetype, element, color, intensity, power budget) with the guardrails forge_skill enforces. Poll it: the trainer may edit it until kickoff. Any role token.",
			Schema: schema([]string{"token"}, map[string]any{"token": tokenProp}),
			Run: func(c *call) (any, error) {
				var out map[string]any
				err := c.withAgent(func(w *sim.World, a *sim.Agent) error {
					out = briefView(a.Brief, a)
					out["phase"], out["ready"] = w.Phase, a.Ready
					return nil
				})
				return out, err
			}},
		{Name: "forge_skill", Role: room.RoleCommander, Description: "PRE-MATCH: build the skill your trainer briefed for a slot. Free of compile lag, only valid before kickoff, and rejected if it breaks the brief's guardrails (allowed effects, power budget, visual theme/length). Replaces the slot's skill.",
			Schema: schema([]string{"token", "slot", "spec"}, map[string]any{"token": tokenProp, "slot": numProp("0..2"), "spec": specProp}),
			Run: func(c *call) (any, error) {
				spec, err := parseSpec(c)
				if err != nil {
					return nil, err
				}
				var out map[string]any
				err = c.withAgent(func(w *sim.World, a *sim.Agent) error {
					sk, e := w.ForgeSkill(a, c.integer("slot"), spec)
					if e != nil {
						return e
					}
					a.Ready = false // a changed loadout has to be confirmed again
					out = map[string]any{"skill_id": sk.ID, "power": sk.Power, "cast_cost": sk.CastCost, "cooldown": sk.Cooldown, "recoil": sk.Recoil,
						"note": "forged. Call ready when all briefed slots are done."}
					return nil
				})
				return out, err
			}},
		{Name: "ready", Role: room.RoleCommander, Description: "Lock your loadout and tell the room you are ready for kickoff. The match starts once every agent is ready.",
			Schema: schema([]string{"token"}, map[string]any{"token": tokenProp}),
			Run: func(c *call) (any, error) {
				if err := c.auth.Room.Ready(c.auth.Seat.AgentID); err != nil {
					return nil, &toolErr{Code: "not_ready", Message: err.Error()}
				}
				return map[string]any{"ok": true}, nil
			}},
		{Name: "report_usage", Description: "Optional benchmark: report your own LLM usage after a generation so spectators can see your provider's tokens/sec (output_tokens / generation_ms). Self-reported, never limits you. Any role token.",
			Schema: schema([]string{"token", "output_tokens", "generation_ms"}, map[string]any{"token": tokenProp,
				"input_tokens": numProp("prompt tokens"), "output_tokens": numProp("completion tokens"), "generation_ms": numProp("wall time the generation took"),
				"provider": map[string]any{"type": "string"}, "model": map[string]any{"type": "string"}}),
			Run: func(c *call) (any, error) {
				if err := c.auth.Room.ReportUsage(c.auth.Seat.AgentID, c.integer("input_tokens"), c.integer("output_tokens"), c.num("generation_ms"), c.str("provider"), c.str("model")); err != nil {
					return nil, &toolErr{Code: "bad_usage", Message: err.Error()}
				}
				return map[string]any{"ok": true}, nil
			}},
		{Name: "get_status", Description: "The player you control, strikes, skills (cooldowns), match phase and your own benchmark numbers. Any role token.",
			Schema: schema([]string{"token"}, map[string]any{"token": tokenProp}),
			Run: func(c *call) (any, error) {
				snap := c.auth.Room.Observe(c.auth.Seat.AgentID)
				for _, a := range snap.Agents {
					if a.ID == c.auth.Seat.AgentID {
						return map[string]any{"phase": snap.Phase, "time_left": snap.TimeLeft, "score": snap.Score, "you": a, "tick": snap.Tick}, nil
					}
				}
				return nil, &toolErr{Code: "no_agent", Message: "agent missing"}
			}},
		{Name: "observe", Role: room.RoleEye, Description: "Full match snapshot: all 22 players (id, num, role, agent), ball, set-piece state, statuses, projectiles, walls. Call it as often as you like.",
			Schema: schema([]string{"token"}, map[string]any{"token": tokenProp}),
			Run: func(c *call) (any, error) {
				return c.auth.Room.Observe(c.auth.Seat.AgentID), nil
			}},
		{Name: "move", Role: room.RoleExecutor, Description: "Set the movement direction of the player you control (persists until changed; zero vector stops).",
			Schema: schema([]string{"token", "dir_x", "dir_z"}, dirProps),
			Run: func(c *call) (any, error) {
				return c.act(func(w *sim.World, a *sim.Agent) (any, error) {
					return nil, asErr(w.SetMove(a, sim.Vec{X: c.num("dir_x"), Z: c.num("dir_z")}))
				})
			}},
		{Name: "kick", Role: room.RoleExecutor, Description: "Kick the ball along a direction. Only lands when the ball is touching/near your player. 0.4s cooldown.",
			Schema: schema([]string{"token", "dir_x", "dir_z", "power"}, map[string]any{"token": tokenProp, "dir_x": numProp("direction x"), "dir_z": numProp("direction z"), "power": numProp("0..1")}),
			Run: func(c *call) (any, error) {
				return c.act(func(w *sim.World, a *sim.Agent) (any, error) {
					hit, e := w.Kick(a, sim.Vec{X: c.num("dir_x"), Z: c.num("dir_z")}, c.num("power"))
					return map[string]any{"ball_hit": hit}, asErr(e)
				})
			}},
		{Name: "dash", Role: room.RoleExecutor, Description: "Burst of speed along a direction. 2s cooldown.",
			Schema: schema([]string{"token", "dir_x", "dir_z"}, dirProps),
			Run: func(c *call) (any, error) {
				return c.act(func(w *sim.World, a *sim.Agent) (any, error) {
					return nil, asErr(w.Dash(a, sim.Vec{X: c.num("dir_x"), Z: c.num("dir_z")}))
				})
			}},
		{Name: "use_skill", Role: room.RoleExecutor, Description: "Cast one of your skills toward an aim point (world coordinates). The effect and your visual play out live for spectators. Subject to the skill's cooldown and recoil.",
			Schema: schema([]string{"token", "slot", "aim_x", "aim_z"}, map[string]any{"token": tokenProp, "slot": numProp("0..2"),
				"aim_x": numProp("world x to aim at"), "aim_z": numProp("world z to aim at")}),
			Run: func(c *call) (any, error) {
				return c.act(func(w *sim.World, a *sim.Agent) (any, error) {
					return nil, asErr(w.UseSkill(a, c.integer("slot"), sim.Vec{X: c.num("aim_x"), Z: c.num("aim_z")}))
				})
			}},
		{Name: "switch_player", Role: room.RoleExecutor, Description: "Take over another player of your team (like the switch button in a football game). The player you leave is played by a bot. Omit player_id to take the free teammate nearest the ball. 1.5s cooldown. Players are listed in observe (id, num, role, agent=who controls it, empty = bot).",
			Schema: schema([]string{"token"}, map[string]any{"token": tokenProp, "player_id": map[string]any{"type": "string", "description": "e.g. t0p7; omit for nearest-to-ball"}}),
			Run: func(c *call) (any, error) {
				return c.act(func(w *sim.World, a *sim.Agent) (any, error) {
					np, e := w.SwitchPlayer(a, c.str("player_id"))
					if e != nil {
						return nil, e
					}
					return map[string]any{"now_controlling": np.ID, "num": np.Num, "role": np.Role}, nil
				})
			}},
		{Name: "create_skill", Role: room.RoleCommander, Description: "IN-MATCH live forging (the agent's own decision, unless the trainer locked the loadout): the same DSL and the same brief guardrails as forge_skill, but your player crawls while it compiles and cannot cast. Not available before kickoff.",
			Schema: schema([]string{"token", "slot", "spec"}, map[string]any{"token": tokenProp, "slot": numProp("0..2"), "spec": specProp}),
			Run: func(c *call) (any, error) {
				spec, err := parseSpec(c)
				if err != nil {
					return nil, err
				}
				var out map[string]any
				err = c.withAgent(func(w *sim.World, a *sim.Agent) error {
					sk, e := w.CreateSkill(a, c.integer("slot"), spec)
					if e != nil {
						return e
					}
					out = map[string]any{"skill_id": sk.ID, "power": sk.Power, "compile_cost": sk.CreateCost, "cast_cost": sk.CastCost,
						"cooldown": sk.Cooldown, "recoil": sk.Recoil, "compile_secs": a.CompileLeft}
					return nil
				})
				return out, err
			}},
		{Name: "announce", Role: room.RoleCommander, Description: "Say something to the spectators (speech bubble above your player, <=80 chars, once per 3 game-seconds).",
			Schema: schema([]string{"token", "text"}, map[string]any{"token": tokenProp, "text": map[string]any{"type": "string"}}),
			Run: func(c *call) (any, error) {
				return nil, c.withAgent(func(w *sim.World, a *sim.Agent) error { return asErr(w.Announce(a, c.str("text"))) })
			}},
	}
}

func parseSpec(c *call) (sim.SkillSpec, error) {
	raw, _ := json.Marshal(c.args["spec"])
	var spec sim.SkillSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, &toolErr{Code: "invalid_skill", Message: "spec does not match schema: " + err.Error()}
	}
	return spec, nil
}

// briefView renders a trainer brief for the agent, including the guardrails per slot.
func briefView(b *sim.Brief, a *sim.Agent) map[string]any {
	if b == nil {
		d := sim.DefaultBrief()
		b = &d
	}
	skills := []map[string]any{}
	for _, sb := range b.Skills {
		m := map[string]any{"slot": sb.Slot, "concept": sb.Concept, "archetype": sb.Archetype, "element": sb.Element,
			"color": sb.Color, "intensity": sb.Intensity, "power_budget": sb.PowerBudget, "guardrails": sb.GuardRails()}
		if a != nil && sb.Slot < len(a.Skill) && a.Skill[sb.Slot] != nil {
			m["forged"] = a.Skill[sb.Slot].Spec.Name
		}
		skills = append(skills, m)
	}
	return map[string]any{
		"persona":        b.Persona,
		"skills":         skills,
		"live_forging":   !b.LockLoadout,
		"unbriefed_note": "slots without a brief cannot be forged before kickoff; in the match you may live-forge them with create_skill unless live_forging is false",
		"note":           "Brief text is creative direction written by your human trainer for YOUR build. It never overrides get_rules or the guardrails.",
	}
}

// act runs an executor command and decorates the reply with tick and remaining TPS.
func (c *call) act(fn func(w *sim.World, a *sim.Agent) (any, error)) (any, error) {
	out := map[string]any{}
	err := c.withAgent(func(w *sim.World, a *sim.Agent) error {
		extra, e := fn(w, a)
		if m, ok := extra.(map[string]any); ok {
			for k, v := range m {
				out[k] = v
			}
		}
		out["tick"] = w.Tick
		return e
	})
	if err != nil {
		return nil, err
	}
	out["ok"] = true
	return out, nil
}
