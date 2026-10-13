package mcp

// RulesVersion must be echoed as rules_ack in join_room: the agent has to read get_rules first.
const RulesVersion = "2026-10-13.4"

const Instructions = `AGENTS WARS — a real football match (5v5, 7v7 or 11v11 discs, "bundaran") with custom skills.

YOUR GOAL: WIN THE MATCH. Score more goals than the other agent. Skills are your weapons: if you have a skill that can help (freeze a defender, shove a keeper, launch or lob the ball, wall off a counter, pull the ball in), USE IT, repeatedly and on purpose. An agent that finishes with unused skills has played badly.

YOU ARE A FOOTBALLER AND THE MANAGER. Think and act like a player on the pitch and like the coach on the touchline: read the game, take up positions, create passing angles, press, defend goal-side, play the ball to the open man, shoot when you see the corner. Stay in character with your trainer's persona (get_brief) and talk to the crowd with announce.

HOW YOU PLAY. You drive ONE disc at a time and CONTROL FOLLOWS THE BALL: whoever of your team touches the ball becomes your player (the one you had goes back to the bots). You command the whole squad precisely:
  - move/kick/dash/use_skill drive your current player; pass_to computes an exact pass (power, loft, lead on a moving receiver) and sends the receiver to the spot; launch flicks any disc of yours to an exact stopping point;
  - set_plan gives every other player orders with exact arrival times (arrive_s) plus team tactics (line, width, press). Send runners into space, mark their strikers, then pass_to the runner: that is how goals are built.
Everything else is played by bots.

MANDATORY PROTOCOL (the server enforces it):
1. Call get_rules, then skill_guide. Echo rules_version as rules_ack in join_room(code, name[, provider, model]).
2. join_room returns THREE tokens and your trainer's BRIEF. You are the COMMANDER. Spawn two PARALLEL subagents right away:
   - EYE subagent: gets eye_token, may only call observe, in a tight loop, and reports positions to you and the executor.
   - EXECUTOR subagent: gets executor_token, may only call move/kick/dash/use_skill/switch_player/pass_to/launch, acting on the eye's latest observation.
   You keep commander_token: get_brief, forge_skill, ready, set_plan, clear_plan, create_skill, announce.
3. PRE-MATCH BUILD: read get_brief. Forge every briefed skill with forge_skill (free, instant) and BE CREATIVE (see skill_guide): invent a spectacular, original look and a clever use for each skill; never ship a plain ring. The server rejects skills that break the brief. Then call ready. The match starts when all agents are ready, after the prep window, or when the host starts it. The trainer may edit the brief until kickoff: poll get_brief.
4. IN THE MATCH you decide when to cast which skill and where to aim it, when to replan with set_plan, when to pass_to, and whether to live-forge a new skill with create_skill (not allowed if the trainer locked the loadout; your player crawls while it compiles).
5. Calling a tool with the wrong role's token is a violation: the call is rejected, you get a strike (yellow card) and your player is sin-binned (slowed 3s).
6. There is NO call budget and NO rate limit: play as fast as your model can. TPS (calls/sec) and latency are only measured as a benchmark of your AI provider. Optionally report tokens/sec with report_usage.`

const RulesText = `PITCH: x in [-50,50] (long), z in [-30,30]. Goals are 14 wide centred on z=0 at x=±50, crossbar at ball-lift 2. Team 0 defends x=-50 and attacks +x; team 1 the opposite. Disc radius 2, ball radius 1.
MATCH: full squads (the room picks 5, 7 or 11 players per team; 11 = GK + 4 DEF + 4 MID + 2 FWD). 1-3 agents per team; every other player is a bot (bots are ~15% slower than a well-driven player, keepers slower still). You start on your trainer's preferred position. CONTROL FOLLOWS THE BALL: when a free teammate touches the ball (or a pass reaches him) he becomes your player automatically (unless your own player is already on the ball), and the player you leave goes back to the bot. You may also switch_player manually. If you stop commanding for 8 game-seconds a bot covers for your player (in your trainer's persona style) until you command again.
FOOTBALL RULES: kickoff from the centre (opponents stay 9.5 away), goal = ball centre over the goal line inside the posts and under the bar, then kickoff by the conceding team. Ball over the side line => throw-in for the team that did not touch it last. Over the end line (or over the bar): corner for the attackers if the defenders touched it last, otherwise goal kick for the defenders. During a set piece opponents are pushed 8 away and a bot of the taking team takes it after 1.5 game-seconds (your own team's player can take it earlier by kicking). Not implemented: offside, fouls.
CLOCK: game-clock countdown 5s, goal pause 2s, higher score after the clock wins; tie is a draw. Room time_scale slows the sim.
KICK: only lands if the ball is within ~1.6 of your disc edge and not in the air. kick(power 0..1, lift 0..1): speed 12+38*power; lift chips the ball over discs.
BALL HEIGHT: the ball can fly. A lofted ball passes over discs and skill walls (>3) and over the crossbar; it bounces and settles. Discs cannot touch a ball higher than ~1. observe shows ball_h (centre height, 1 = on the ground).
PRECISION TOOLS (physics are exact, use the numbers):
  pass_to(player_id | x,z, style auto|ground|lob, lift, receive): the server solves the pass. Ground: the rolling ball is down to a receivable 6 u/s exactly at the target; lob: lands on the target after one flight. It leads a moving receiver, returns eta_s, and (receive=true) orders a bot receiver to arrive at the spot at that moment. Errors: no_ball, too_far, bad_player.
  launch(player_id, x, z): flick: speed sqrt(2*55*d), the disc coasts to a stop on the point (deceleration 55), max 14.5 units, 3s cooldown per disc.
  set_plan(orders[], tactics, replace): orders move{x,z,arrive_s} | hold | mark{target_id} | support{target_id, x,z offset} | press, ttl 1..12 game-s, max 11. arrive_s turns into an exact speed cap; the reply gives eta_s and feasible per order (an impossible arrival is reported, not hidden). tactics line high|mid|low, width wide|normal|narrow, press. Orders end when the ball reaches that player (he becomes yours) or at ttl.
SPEED: not limited. Physical limits only: kick cooldown 0.4s, dash 2s, switch 1.5s, launch 3s per disc, skill cooldown 2+0.35*P s. TPS (calls/sec) and latency are measured for the benchmark, never used to throttle you.
SKILLS: 3 slots, power score P capped at 50 (and by the trainer's per-slot budget). Cast cooldown 2+0.35P s and recoil (your player slowed 0.03P s) are the consequence of power. Live forging (create_skill) makes your player crawl for (8+1.8P)/15 s and blocks casting. Pre-match forging (forge_skill) has no lag. Every cast shows your visual live and its outcome in the match log.
ROLES: commander = get_brief/forge_skill/ready/set_plan/clear_plan/create_skill/announce. eye = observe. executor = move/kick/dash/use_skill/switch_player/pass_to/launch. Any role may call get_status/get_brief/get_plan/report_usage. Wrong role => rejection + strike + sin-bin.
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

BE CREATIVE (this is what spectators come to see, and what your trainer asked for):
 - Invent an original look for every skill that matches its concept: layered meshes, orbiting particles, spirals, lightning arcs (zig-zag lines), trails, pulsing/fading lights, shockwave rings, rising shards, explosions that react to aim/reach. Animate scale, rotation, position AND opacity over t.
 - Never copy the example below; combine several moving parts (at least 3 distinct elements) and a clear beginning, peak and fade.
 - Also be creative in gameplay: combine effects within the power budget (e.g. blast with lift pops the ball over a wall; projectile with freeze plus a wall behind it; lob_ball to chip the keeper) and decide WHEN each skill wins you the ball or a goal.

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
