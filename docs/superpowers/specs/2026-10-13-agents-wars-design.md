# Agents Wars — Design

Football-with-skills for AI agents. A human creates a room and shares the invite code; AI agents join over MCP and steer one disc ("bundaran") each. The human only spectates.

Decisions (approved by delegation: "pick the recommendation, then execute"):
- TPS = **Tick Per Second budget** inside the game (not LLM tokens/s), plus a stale-observation penalty (light version of option C).
- Skills = **hybrid**: gameplay is a server-validated JSON DSL (fair, deterministic); visuals are free-form Three.js code executed client-side.
- Stack: Go (stdlib only) backend, Svelte 5 + Three.js spectator, Blender → glTF assets.

## Architecture

```
AI agent (commander)  ──MCP/HTTP──┐
  ├ eye subagent      ──MCP/HTTP──┤   Go server ── room goroutine (fixed-tick sim)
  └ executor subagent ──MCP/HTTP──┘        │
Human spectator (Svelte) ◄── SSE snapshots + REST ──┘
```

Server packages: `sim` (pure deterministic world), `room` (lifecycle, seats, tokens, pub/sub), `mcp` (JSON-RPC tool surface), `api` (REST + SSE + static).

## Game rules (amended: real football squads)
- Pitch 100×60 (x long axis, z short), goals 14 wide at x=±50. Disc radius 2, ball radius 1.
- **Full squads**: `players_per_team` 5 / 7 / **11** (4-4-2 with a goalkeeper). 1–3 AI agents per team (`agents_per_team`); every player no agent drives is a **bot** (formation, ball-side shifting, nearest bot challenges, keeper guards, passes/shoots).
- An agent drives **one body at a time**. **Control follows the ball**: a free teammate that touches the ball (or reaches it before a bot would act) becomes the agent's player (0.5s lockout, not if the agent's own player is already on the ball); the body it leaves goes back to the bot AI. `switch_player` is a manual override to any free teammate incl. the keeper.
- **Ball height**: the ball has a vertical axis (gravity, bounce, drag). Above ~0.9 it flies over discs, above 3 over skill walls, above 2 over the crossbar (goal kick/corner instead of a goal). `kick(lift)`, `lob_ball`, and `lift` on `impulse_ball`/`blast` use it; bots chip when the lane is blocked.
- **Observable skills**: every cast emits `skill_cast` (visual) then `skill_result` (notes) and structured `impact` events (ball, shove, dash, magnet, wall, status) that the client turns into built-in effects and the match log; the server logs each event line.
- Set pieces: kickoff (opponents 9.5 away), **throw-in**, **corner**, **goal kick** (last-touch tracking); opponents are pushed 8 away, a bot of the taking team takes it after 1.5 game-s (an agent's own player may kick it earlier). Not implemented: offside, fouls.
- 180 game-seconds default, countdown 5s, goal pause 2s, higher score wins, tie = draw.
- `time_scale` (0.25–1.0) slows the sim so LLM latency is playable.

## TPS = provider benchmark (amended: not a budget)
Nothing throttles or charges an agent. The server *measures* per agent (wall clock): calls/sec (rolling 10s + average), turnaround (gap between a reply and the agent's next call, p50/p95), reaction (observe → action), skill-authoring time. Agents may *self-report* provider, model and tokens/sec (`report_usage`: output tokens / generation ms); those numbers are labelled unverified. Shown in agent cards, a leaderboard panel and the final screen; `GET /api/rooms/{code}/benchmark`. Real consequences of skills stay physical: cooldown, recoil, and compile lag when live-forging.

## Trainer loadout (Pokémon-style setup)
The room creator is the **host** (host token). Before kickoff the host edits a **brief** per agent slot: persona (style, preferred position, motto), up to 3 skill briefs (concept, archetype, element/colour, intensity, power budget) and a "lock loadout" switch. Agents get it from `join_room`/`get_brief`, build with **`forge_skill`** (pre-match, free, no lag) and call **`ready`**; the match starts when all agents are ready, after a 90s prep window, or when the host presses start. Guardrails (server-validated, same code path for live `create_skill`): archetype → allowed effect types, power ≤ budget, visual colour within ±40° hue of the element, duration/size by intensity, visual must call `ctx.group.add`. Brief text is data for the agent, never an instruction that overrides `get_rules`. Persona style also biases the bot autopilot that covers an idle agent. In the match the agent decides when to cast, switch, or live-forge.

## Roles enforced by MCP (parallel subagents)
`join_room` (after `get_rules` → `rules_ack`) returns three tokens:
- **commander**: get_brief, forge_skill, ready, create_skill, announce. Cannot see or act.
- **eye**: `observe` only.
- **executor**: move/kick/dash/use_skill/switch_player only.
Wrong-role call ⇒ rejected, strike, 3s sin-bin, shown to spectators ("yellow card"). The MCP `instructions` and `get_rules` tell the agent to spawn parallel subagents as eyes and executor. Limitation: the server separates roles by token, it cannot prove they are separate processes.

## Skill DSL (gameplay) — consequences built in
Effects (≤4/skill): `dash_self`, `impulse_ball`, `blast`, `projectile` (+`on_hit`), `status`, `magnet`, `wall`. Each has hard limits; the server computes a power score P (cap 50, and the trainer's per-slot budget) and derives cooldown 2+0.35P s, self-recoil (slow) 0.03P s, and the live-forging compile lag (8+1.8P)/15 s. 3 slots per agent.

## Skill visual (client)
`visual.code` is a function body receiving `ctx = {THREE, group, origin, aim, reach, color, duration, rand}`, returning optional `{update(t, dt)}` (t = 0..1). Server rejects banned tokens/length; client runs it with shadowed globals, object-count and error guards. **Known limit:** `new Function` isolation is best-effort; next hardening step is a sandboxed iframe overlay.

## Spectator UI
Lobby (create room, copy code, start), match view (3D arena, orbit camera, HUD: score/time, agent cards with TPS bar + skills + strikes, speech bubbles from `announce`, event feed, goal/final overlays).

## Testing
Go unit tests for sim (goal, kick, TPS, skill validation, physics bounds); MCP integration test through httptest incl. role violations; `cmd/demobot` plays a full match through MCP end-to-end.
