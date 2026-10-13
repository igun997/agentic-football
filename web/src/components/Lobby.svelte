<script lang="ts">
  import { createRoom, getRoom } from '../lib/api'

  let squad = $state(11)
  let agents = $state(1)
  let minutes = $state(3)
  let speed = $state(0.5)
  let joinCode = $state('')
  let busy = $state(false)
  let error = $state('')

  async function create() {
    busy = true; error = ''
    try {
      const r = await createRoom({ players_per_team: squad, agents_per_team: agents, duration_secs: minutes * 60, time_scale: speed })
      location.hash = `#/room/${r.code}`
    } catch (e) { error = (e as Error).message } finally { busy = false }
  }
  async function watch() {
    error = ''
    try { await getRoom(joinCode.trim().toUpperCase()); location.hash = `#/room/${joinCode.trim().toUpperCase()}` }
    catch (e) { error = (e as Error).message }
  }
</script>

<main>
  <div class="hero">
    <p class="kicker display">Football · Skills · Agents only</p>
    <h1 class="display">Agents<br /><span>Wars</span></h1>
    <p class="sub">Create a room, hand the invite code to your AI agents, and watch a full football match of discs. Agents coach one player each and swap between teammates; bots play the rest.
      You set up each agent like a Pokémon trainer — persona, position, skill concepts — and the MCP guards the rails. Speed is benchmarked, never throttled.</p>
  </div>

  <section class="card">
    <h2 class="display">New match</h2>
    <label>Players on the pitch <small>(full squads; non-agent players are bots)</small>
      <div class="seg">{#each [5, 7, 11] as n}<button class:on={squad === n} onclick={() => (squad = n)}>{n} v {n}</button>{/each}</div>
    </label>
    <label>AI agents per team <small>(each drives one player and can switch)</small>
      <div class="seg">{#each [1, 2, 3] as n}<button class:on={agents === n} onclick={() => (agents = n)}>{n}</button>{/each}</div>
    </label>
    <label>Match length
      <div class="seg">{#each [1, 3, 5] as n}<button class:on={minutes === n} onclick={() => (minutes = n)}>{n} min</button>{/each}</div>
    </label>
    <label>Game speed <small>(slower = more time for LLM latency)</small>
      <div class="seg">{#each [[0.25, 'Calm'], [0.5, 'Normal'], [1, 'Real-time']] as [v, n]}<button class:on={speed === v} onclick={() => (speed = v as number)}>{n}</button>{/each}</div>
    </label>
    <button class="go display" disabled={busy} onclick={create}>Create room →</button>

    <div class="or"><span>or watch an existing room</span></div>
    <form onsubmit={(e) => { e.preventDefault(); watch() }}>
      <input placeholder="INVITE CODE" maxlength="6" bind:value={joinCode} />
      <button class="alt display" disabled={joinCode.length < 4}>Watch</button>
    </form>
    {#if error}<p class="err">{error}</p>{/if}
  </section>
</main>

<style>
  main { min-height: 100%; display: grid; grid-template-columns: 1.2fr 1fr; gap: 48px; align-items: center; padding: 6vh 7vw;
    background: radial-gradient(900px 500px at 15% 20%, #123a5a55, transparent), radial-gradient(700px 500px at 90% 90%, #5a122f44, transparent), var(--bg); }
  .kicker { color: var(--gold); font-size: 14px; margin: 0 0 8px; }
  h1 { font-size: clamp(64px, 11vw, 150px); line-height: .85; margin: 0 0 24px; }
  h1 span { color: transparent; -webkit-text-stroke: 2px var(--t0); text-shadow: 0 0 40px #35e0ff55; }
  .sub { color: var(--dim); max-width: 46ch; font-size: 18px; line-height: 1.5; }
  .card { background: var(--panel); border: 1px solid var(--line); padding: 28px; display: grid; gap: 18px; backdrop-filter: blur(8px);
    clip-path: polygon(0 0, calc(100% - 22px) 0, 100% 22px, 100% 100%, 0 100%); }
  h2 { margin: 0; font-size: 20px; }
  label { display: grid; gap: 8px; color: var(--dim); font-size: 13px; text-transform: uppercase; letter-spacing: .06em; }
  label small { text-transform: none; letter-spacing: 0; opacity: .7; }
  .seg { display: flex; gap: 6px; }
  .seg button { flex: 1; padding: 10px; background: #0a1120; color: var(--text); border: 1px solid var(--line); }
  .seg button.on { background: var(--t0); color: #04121a; border-color: var(--t0); font-weight: 700; }
  .go { padding: 16px; background: var(--gold); color: #1a1300; border: 0; font-size: 18px; }
  .go:hover:not(:disabled) { filter: brightness(1.1); }
  .or { text-align: center; color: var(--dim); font-size: 12px; display: flex; align-items: center; gap: 10px; }
  .or::before, .or::after { content: ''; flex: 1; height: 1px; background: var(--line); }
  form { display: flex; gap: 8px; }
  input { flex: 1; padding: 12px; background: #0a1120; color: var(--text); border: 1px solid var(--line); letter-spacing: .3em; font-weight: 700; text-transform: uppercase; }
  .alt { padding: 0 20px; background: transparent; color: var(--t0); border: 1px solid var(--t0); }
  .err { color: var(--t1); margin: 0; }
  @media (max-width: 820px) { main { grid-template-columns: 1fr; } }
</style>
