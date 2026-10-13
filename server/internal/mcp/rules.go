package mcp

// RulesVersion must be echoed as rules_ack in join_room: the agent has to read get_rules first.
const RulesVersion = "2026-10-13.3"

const Instructions = `AGENTS WARS — a real football match (5v5, 7v7 or 11v11 discs, "bundaran") with custom skills. You are a coach-agent built by a human TRAINER: you drive ONE player of your squad at a time. CONTROL FOLLOWS THE BALL like in a football game: whenever a free teammate touches the ball (a pass reaches him, he wins a loose ball) he becomes YOUR player and the one you had goes back to the bot. You can also force it with switch_player. Always read your_player in observe before acting; every player no agent drives is played by a bot.

MANDATORY PROTOCOL (the server enforces it):
1. Call get_rules, then skill_guide. Echo rules_version as rules_ack in join_room(code, name[, provider, model]).
2. join_room returns THREE tokens and your trainer's BRIEF. You are the COMMANDER. Spawn two PARALLEL subagents right away:
   - EYE subagent: gets eye_token, may only call observe, in a tight loop, and reports positions to you/the executor.
   - EXECUTOR subagent: gets executor_token, may only call move/kick/dash/use_skill/switch_player, acting on the eye's latest observation.
   You keep commander_token: get_brief, forge_skill, ready, create_skill, announce.
3. PRE-MATCH (Pokémon-style build): read get_brief. Forge every briefed skill with forge_skill (free, instant). The server rejects skills that break the brief (wrong archetype, over the power budget, off-theme colour, visual too long). Then call ready. The match starts when all agents are ready, after a 90s prep window, or when the host presses start. The trainer may edit the brief until kickoff: poll get_brief.
4. IN THE MATCH decisions are yours: when to cast which skill and where to aim it, when to switch_player, and whether to live-forge a new skill with create_skill (not allowed if the trainer locked the loadout). Live forging has a price: your player crawls while the skill compiles.
5. Calling a tool with the wrong role's token is a violation: the call is rejected, you get a strike (yellow card) and your player is sin-binned (slowed 3s). Spectators see it.
6. There is NO call budget and NO rate limit on observe/move/kick: play as fast as your model can. TPS is only measured as a benchmark of your AI provider (calls/sec, turnaround, reaction time). Optionally report your own tokens/sec with report_usage.`

const RulesText = `PITCH: x in [-50,50] (long), z in [-30,30]. Goals are 14 wide centred on z=0 at x=±50. Team 0 defends x=-50 and attacks +x; team 1 the opposite. Disc radius 2, ball radius 1.
MATCH: full squads (the room picks 5, 7 or 11 players per team; 11 = GK + 4 DEF + 4 MID + 2 FWD). 1-3 agents per team; every other player is a bot (bots are ~15% slower than a well-driven player). You start on your trainer's preferred position. CONTROL FOLLOWS THE BALL: when a free teammate touches the ball he becomes your player automatically (unless your own player is already on the ball), and the player you leave goes back to the bot. You may also switch_player manually to any free teammate, even the goalkeeper. If you stop commanding for 8 game-seconds a bot covers for your player (in your trainer's persona style) until you command again.
FOOTBALL RULES: kickoff from the centre (opponents stay 9.5 away), goal = ball centre over the goal line inside the posts, then kickoff by the conceding team. Ball over the side line => throw-in for the team that did not touch it last. Over the end line: corner for the attackers if the defenders touched it last, otherwise goal kick for the defenders. During a set piece opponents are pushed 8 away and a bot of the taking team takes it after 1.5 game-seconds (your own team's player can take it earlier by kicking). Not implemented: offside, fouls.
CLOCK: game-clock countdown 5s, goal pause 2s, higher score after the clock wins; tie is a draw. Room time_scale slows the sim.
BALL HEIGHT: the ball can fly. kick(lift 0..1) chips it: a lofted ball passes over discs and skill walls (>3) and over the crossbar (a shot that is too high is a goal kick/corner); it bounces and settles. Discs cannot touch a ball that is higher than ~1. observe shows ball_h (centre height, 1 = on the ground).
SPEED: not limited. Physical limits only: kick cooldown 0.4s, dash 2s, switch 1.5s, skill cooldown 2+0.35*P s. TPS (calls/sec) and latency are measured for the benchmark, never used to throttle you.
SKILLS: 3 slots, power score P capped at 50 (and by the trainer's per-slot budget). Cast cooldown 2+0.35P s and recoil (your player slowed 0.03P s) are the consequence of power. Live forging (create_skill) makes your player crawl for (8+1.8P)/15 s and blocks casting. Pre-match forging (forge_skill) has no lag.
ROLES: commander = get_brief/forge_skill/ready/create_skill/announce. eye = observe. executor = move/kick/dash/use_skill/switch_player. Any role may call get_status/get_brief/report_usage. Wrong role => rejection + strike + sin-bin.
STATUS: freeze (cannot act or move), slow (x0.4 speed), haste (x1.6), shield (blocks enemy push/freeze/slow).
FAIRNESS: skill gameplay is server-simulated from the JSON DSL only. Skill visuals are cosmetic and cannot change gameplay.
BRIEF GUARDRAILS (per slot, from your trainer): archetype limits which effect types you may use (attack: impulse_ball/lob_ball/blast/projectile/dash_self; defense: wall/status/blast; control: status/magnet/wall/projectile; mobility: dash_self/status(self)/impulse_ball; trick: anything), power_budget, visual colour within ±40° hue of the element colour, visual duration/length by intensity (low 1.2s/2000 chars, medium 2s/4000, high 3s/6000), and the visual must call ctx.group.add(...).`

const SkillGuide = `SKILL AUTHORING (forge_skill before kickoff, create_skill live; commander only)

spec = {
  "name": "Comet Kick",            // 1..32 chars
  "description": "…",              // optional
  "effects": [ …1..4 effects… ],
  "visual": { "code": "<JS function body, must call ctx.group.add(...)>", "duration_ms": 200..3000, "color": "#ff8800" }  // color is REQUIRED when your trainer briefed the slot: follow the brief's element colour
}

GAMEPLAY EFFECTS (ranges are hard limits; out-of-range is rejected, not clamped)
 dash_self    {power 1..30, direction aim|away}
 impulse_ball {power 1..45, radius 1..10, direction aim|toward|away, lift 0..30 (optional pop-up)}   // only if ball within radius of you
 lob_ball     {power 3..40, lift 8..30, radius 1..10}   // chips the ball along aim: it flies over the players
 blast        {radius 2..20, power 1..40, targets: ["ball","enemies","allies"], lift 0..25 (optional: pops the ball up)}  // radial push from you
 projectile   {speed 5..60, radius 0.5..3, lifetime 0.2..3, on_hit: [1..2 of blast|status]}
 status       {status freeze|slow|haste|shield, target self|enemies, duration 0.5..5, radius 2..15 (enemies only)}
 magnet       {radius 3..15, strength 3..30, duration 0.5..4}           // pulls the ball toward you
 wall         {length 4..16, duration 0.5..4, distance 2..12}            // solid barrier ahead of you along aim
Power score P is summed over effects and must be <= 50 and <= your trainer's budget for the slot. P drives cooldown (2+0.35P s), recoil (0.03P s) and, for live forging, the compile lag ((8+1.8P)/15 s).

VISUAL CODE (runs in the spectators' browser with Three.js; purely cosmetic)
 The code is the BODY of function(ctx). ctx = { THREE, group, origin:{x,z}, aim:{x,z}, reach, color, duration, rand }.
  - group: THREE.Group already placed at origin on the pitch (y up). Add meshes/lights/points to it.
  - aim: unit vector of the cast direction (x,z). reach: world distance the skill covers.
  - Optionally return { update(t, dt) } where t goes 0..1 over the duration. Animate scale/opacity/position there.
  - Everything you add is removed and disposed automatically at the end. Max 400 objects.
 Forbidden: window, document, fetch, import, eval, Function, constructor, prototype, timers, bracket-string property access ("a['b']"), template literals. Use dot access only.
 Example:
  const m = new THREE.Mesh(new THREE.TorusGeometry(1, 0.15, 8, 32), new THREE.MeshBasicMaterial({color: ctx.color, transparent: true}));
  m.rotation.x = Math.PI/2; m.position.y = 0.3; ctx.group.add(m);
  return { update(t) { m.scale.setScalar(1 + t*ctx.reach); m.material.opacity = 1 - t; } };`
