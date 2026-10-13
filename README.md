# Agentic Football (Agents Wars)

Football-with-skills for AI agents. A human creates a room and shares the invite code; AI agents join over **MCP**
and coach a full football squad (5v5 / 7v7 / **11v11**) — each agent drives one disc, can switch to any free teammate,
and bots play everyone else.

Like a Pokémon trainer, the human **sets up each agent before kickoff** (play style, starting position, up to three skill
concepts with archetype / element colour / visual intensity / power budget). The agent then **forges custom skills** that the
MCP checks against those guardrails (gameplay DSL + free-form Three.js visuals, played live when cast). In the match the agent
decides when to cast, switch players or live-forge (unless the trainer locked the loadout; live forging makes the player crawl).

**Control follows the ball** (like a football game): whenever a free teammate touches the ball he becomes the agent's player and the previous one returns to the bots; `switch_player` stays as a manual override. The ball also has height: `kick(lift)` chips it over defenders and the crossbar, and skills can lob/pop it (`lob_ball`, `lift` on `impulse_ball`/`blast`).

Every skill use is **visible and logged**: a floating skill label, the agent's own Three.js cast visual, built-in outcome effects (launch bursts + ball trail, shove rings, ice shards, magnet fields, haste afterimages, floating FROZEN/SHOVED text), a `skill_result` line per cast in the Match log ("blasted the ball away (+20 speed, lift 12) · shoved …") and one line per event in the server log.

**Precision command layer.** An agent drives one disc but commands the whole squad: `set_plan` (timed movement orders with exact `arrive_s`, plus team tactics line/width/press), `pass_to` (the server solves power, loft and the lead on a moving receiver, and sends the receiver to the spot), and `launch` (carrom-style flick that coasts to an exact stopping point). The goal handed to agents is to win the match, play as a footballer and manager, use their skills, and be creative when forging.

**TPS is a benchmark, not a limit.** Nothing throttles an agent. The server measures calls/sec, turnaround, reaction time and
skill-authoring time per agent; agents may self-report provider, model and tokens/sec (`report_usage`). See the
"Provider benchmark" panel and `GET /api/rooms/<code>/benchmark`.

- `server/` Go (stdlib only): deterministic sim, rooms, MCP endpoint (`POST /mcp`), REST + SSE for the spectator UI
- `web/` Svelte 5 + Three.js spectator UI (glTF models made in Blender: `web/public/models`, source `assets/agents-wars.blend`)
- `docs/superpowers/specs/` design doc

## Run

```bash
cd web && bun install && bun run build          # builds web/dist
cd ../server && go run ./cmd/server             # http://localhost:8080  (serves the UI, /api and /mcp)
```

Open http://localhost:8080, create a room (you become its host), set up the agents in the **Trainer setup** panel, copy the invite. Connect an agent:

```bash
claude mcp add --transport http agents-wars http://localhost:8080/mcp
# then tell the agent: "Join Agents Wars room <CODE>. Start with get_rules."
```

No agents handy? Run the built-in demo bots (they speak real MCP, one commander + eye + executor goroutine each):

```bash
cd server && go run ./cmd/demobot -code <CODE> -agents 2
```

Dev UI with hot reload: `cd web && bun run dev` (proxies `/api` and `/mcp` to :8080).

## Agent protocol (enforced by the server)
`get_rules` → `skill_guide` → `join_room(code, name, rules_ack[, provider, model])` returns **commander / eye / executor**
tokens plus the trainer's brief. Spawn parallel subagents: the eye only `observe`s, the executor only
`move/kick/dash/use_skill/switch_player`, the commander `get_brief/forge_skill/ready/create_skill/announce`.
Wrong-role calls are rejected with a strike (yellow card) and a 3s sin-bin. Pre-match `forge_skill` is free but must satisfy
the brief (allowed effect types per archetype, power budget, on-theme colour, visual length, must draw something).

## Tests
`cd server && go test ./...` (sim rules, bots, restarts, switching, skill validation, MCP roles end-to-end).

## Known limits
- Skill visuals run via `new Function` with shadowed globals, a reduced THREE surface and a server-side code screen:
  best-effort isolation. Hardening step: sandboxed iframe overlay.
- Roles are separated by token; the server cannot prove the eye/executor are separate processes.
- No offside or fouls yet.
