<script lang="ts">
  import { onMount } from 'svelte'
  import { Arena, TEAM_COLORS } from '../lib/game/arena'
  import { getRoom, hostToken, startRoom, streamRoom } from '../lib/api'
  import BenchTable from './BenchTable.svelte'
  import TrainerSetup from './TrainerSetup.svelte'
  import type { GameEvent, RoomInfo, SkillInfo, Snapshot } from '../lib/types'

  let { code }: { code: string } = $props()

  let canvas: HTMLCanvasElement
  let overlay: HTMLDivElement
  let info = $state<RoomInfo | null>(null)
  let snap = $state<Snapshot | null>(null)
  let feed = $state<{ id: number; text: string; kind: string }[]>([])
  let banner = $state('')
  let error = $state('')
  let copied = $state(false)
  let showBench = $state(false)
  let arena: Arena | null = null
  const isHost = $derived(!!hostToken(code))
  let seq = 0
  let bannerTimer: ReturnType<typeof setTimeout>

  const mcpUrl = `${location.origin}/mcp`
  const nameOf = (id: string) => snap?.agents.find((a) => a.id === id)?.name ?? info?.seats.find((s) => s.agent_id === id)?.name ?? id
  const bodyLabel = (id?: string) => { const p = snap?.players.find((x) => x.id === id); return p ? `#${p.num} ${p.role}` : '' }
  const teamAgents = (t: number) => (snap?.agents ?? []).filter((a) => a.team === t)
  const RESTART: Record<string, string> = { kickoff: 'KICK OFF', throw_in: 'THROW-IN', corner: 'CORNER', goal_kick: 'GOAL KICK' }
  const clock = $derived.by(() => {
    const t = Math.max(0, Math.ceil(snap?.time_left ?? 0))
    return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, '0')}`
  })
  const inviteText = $derived(
    `Join the Agents Wars football match.\nMCP server: ${mcpUrl}\nInvite code: ${code}\nStart with get_rules and follow the protocol it gives you.`,
  )

  function describe(e: GameEvent): { text: string; kind: string } | null {
    const d = e.data ?? {}
    switch (e.type) {
      case 'join': return { text: `${d.name} joined team ${d.team === 0 ? 'CYAN' : 'RED'}`, kind: 'info' }
      case 'goal': return { text: `GOAL — ${d.label} scores for ${d.team === 0 ? 'CYAN' : 'RED'}!`, kind: 'goal' }
      case 'switch': return { text: `${nameOf(d.agent)} switched to #${d.num} ${d.role}`, kind: 'cast' }
      case 'restart': return d.kind === 'kickoff' ? null : { text: `${RESTART[d.kind]} for ${d.team === 0 ? 'CYAN' : 'RED'}`, kind: 'info' }
      case 'skill_created': return { text: `${nameOf(d.player)} live-forged “${d.name}” (power ${d.power}) — lagging while it compiles`, kind: 'forge' }
      case 'skill_forged': return { text: `${nameOf(d.player)} forged “${d.name}” for slot ${d.slot + 1} (power ${d.power})`, kind: 'forge' }
      case 'ready': return { text: `${nameOf(d.player)} is ready`, kind: 'info' }
      case 'brief_updated': return { text: `Trainer updated ${nameOf(d.player)}'s setup`, kind: 'info' }
      case 'skill_cast': return { text: `${nameOf(d.player)} cast ${d.name}`, kind: 'cast' }
      case 'status': return { text: `${nameOf(d.player)} is ${d.status}`, kind: 'status' }
      case 'strike': return { text: `YELLOW CARD ${nameOf(d.player)}: ${d.reason}`, kind: 'strike' }
      case 'shield_block': return { text: `${nameOf(d.player)}'s shield blocked ${nameOf(d.from)}`, kind: 'status' }
      case 'final': return { text: d.winner === -1 ? 'Full time — draw' : `Full time — ${d.winner === 0 ? 'CYAN' : 'RED'} wins`, kind: 'goal' }
      case 'phase': return null
    }
    return null
  }

  function flash(text: string, ms = 2200) {
    banner = text
    clearTimeout(bannerTimer)
    bannerTimer = setTimeout(() => (banner = ''), ms)
  }

  onMount(() => {
    const arenaLocal = new Arena(canvas, overlay)
    arena = arenaLocal
    let lastSkillsVersion = -1
    let stop = () => {}
    getRoom(code).then(async (r) => {
      info = r
      arenaLocal.setSkills(r.skills)
      lastSkillsVersion = r.snapshot.skills_version
      feed = r.feed.map(describe0).filter(Boolean) as typeof feed
      stop = streamRoom(code, async (s) => {
        snap = s
        arenaLocal.update(s)
        let refetch = false
        for (const e of s.events ?? []) {
          const d = describe(e)
          if (d) feed = [...feed.slice(-40), { id: seq++, ...d }]
          if (['join', 'ready', 'skill_forged', 'brief_updated'].includes(e.type)) refetch = true
          if (e.type === 'goal') flash('GOAL!', 2400)
          if (e.type === 'restart') flash(RESTART[e.data?.kind] ?? '', e.data?.kind === 'kickoff' ? 1200 : 1400)
        }
        if (refetch || s.skills_version !== lastSkillsVersion) {
          lastSkillsVersion = s.skills_version
          const r2 = await getRoom(code)
          info = r2
          arenaLocal.setSkills(r2.skills)
        }
      })
    }).catch((e) => (error = e.message))
    const describe0 = (e: GameEvent) => { const d = describe(e); return d ? { id: seq++, ...d } : null }
    return () => { stop(); arenaLocal.dispose(); clearTimeout(bannerTimer) }
  })

  async function start() {
    try { await startRoom(code) } catch (e) { error = (e as Error).message }
  }
  async function copy() {
    await navigator.clipboard?.writeText(inviteText).catch(() => {})
    copied = true; setTimeout(() => (copied = false), 1500)
  }
  const preview = (sk: SkillInfo) => arena?.previewSkill(sk)
  const tpsLabel = (b?: { tokens_per_sec: number; calls_per_sec: number }) => !b ? '—' : b.tokens_per_sec ? `${b.tokens_per_sec.toFixed(0)} tok/s` : `${b.calls_per_sec.toFixed(1)} calls/s`
</script>

<div class="stage">
  <canvas bind:this={canvas}></canvas>
  <div class="overlay" bind:this={overlay}></div>

  <header>
    <a href="#/" class="back display">← Lobby</a>
    <div class="score">
      <span class="t0 display">{snap?.score[0] ?? 0}</span>
      <div class="mid"><b class="display">{clock}</b><small>{snap?.phase === 'lobby' ? 'waiting for agents' : snap?.phase === 'countdown' ? `kickoff in ${Math.ceil(snap?.phase_left ?? 0)}` : snap?.phase}</small></div>
      <span class="t1 display">{snap?.score[1] ?? 0}</span>
    </div>
    <span class="code display">{code}</span>
  </header>

  {#each [0, 1] as t}
    <aside class={`team team${t}`}>
      {#each teamAgents(t) as p (p.id)}
        <div class="card" style:--team={TEAM_COLORS[t]}>
          <div class="row"><b class="display">{p.name}</b>
            {#if p.strikes}<span class="yc" title="strikes">{'■'.repeat(p.strikes)}</span>{/if}</div>
          <div class="ctl">driving {bodyLabel(p.controls)}</div>
          <div class="tps" title="TPS benchmark: self-reported tokens/s, otherwise measured MCP calls/s. Never limits the agent."><span class="num display">{tpsLabel(p.bench)}</span>
            <small>{p.bench?.provider || 'provider ?'}{p.bench?.model ? ' · ' + p.bench.model : ''}</small></div>
          <div class="chips">
            {#if p.style}<em>{p.style}</em>{/if}
            {#if snap?.phase === 'lobby'}<em class:warn={!p.ready}>{p.ready ? 'ready' : 'preparing'}</em>{/if}
            {#if (p.compile_left ?? 0) > 0}<em class="warn">compiling {p.compile_left?.toFixed(1)}s</em>{/if}
            {#each Object.entries(snap?.players.find((x) => x.id === p.controls)?.status ?? {}) as [k]}<em>{k}</em>{/each}
          </div>
          <div class="skills">
            {#each [0, 1, 2] as slot}
              {@const sk = p.skills?.find((s) => s.slot === slot)}
              <div class="sk" class:empty={!sk} title={sk ? `power ${sk.power} · cd ${sk.cooldown}s` : 'empty slot'}>
                {#if sk}<span>{sk.name}</span><i style:width={(sk.cd_left > 0 ? (1 - sk.cd_left / sk.cooldown) * 100 : 100) + '%'}></i>{:else}<span>—</span>{/if}
              </div>
            {/each}
          </div>
        </div>
      {/each}
    </aside>
  {/each}

  <ul class="feed">
    {#each feed.slice(-7) as f (f.id)}<li class={f.kind}>{f.text}</li>{/each}
  </ul>

  <button class="benchbtn display" onclick={() => (showBench = !showBench)}>{showBench ? 'Hide' : 'Provider'} benchmark</button>
  {#if showBench && snap}<div class="benchpanel"><BenchTable agents={snap.agents} compact /></div>{/if}

  {#if banner}<div class="banner display">{banner}</div>{/if}

  {#if snap?.phase === 'finished'}
    <div class="final">
      <p class="display">Full time</p>
      <h2 class="display">{snap.score[0]} – {snap.score[1]}</h2>
      <p class="display win" style:color={snap.winner >= 0 ? TEAM_COLORS[snap.winner] : 'var(--gold)'}>
        {snap.winner === -1 ? 'Draw' : `${teamAgents(snap.winner).map((p) => p.name).join(' & ') || (snap.winner === 0 ? 'Cyan bots' : 'Red bots')} win`}</p>
      <div class="benchfinal"><BenchTable agents={snap.agents} compact /></div>
      <a href="#/" class="again display">New match</a>
    </div>
  {/if}

  {#if snap?.phase === 'lobby' && info}
    <div class="lobby">
      <div class="invite">
        <p class="display kick">Invite your agents</p>
        <div class="big display">{code}</div>
        <p class="seats">{info.seats.length} / {info.config.agents_per_team * 2} agents joined · {info.config.players_per_team} v {info.config.players_per_team}</p>
        <ul class="seatlist">
          {#each info.slots as s}<li style:border-color={TEAM_COLORS[s.team]} class:free={!s.agent_id}>{s.agent_id ? `${s.name}${s.ready ? ' ✓' : ''}` : 'open seat'}</li>{/each}
        </ul>
        <pre>{inviteText}</pre>
        <div class="btns">
          <button class="alt display" onclick={copy}>{copied ? 'Copied ✓' : 'Copy invite'}</button>
          {#if isHost}<button class="go display" onclick={start} disabled={info.seats.length < 1}>Start now</button>{/if}
        </div>
        <small>Open seats are played by bots. The match starts when every agent is ready, after a 90s prep window, or when the host starts it.</small>
      </div>
      <TrainerSetup {info} editable={isHost} onSaved={(i) => (info = i)} onPreview={preview} />
    </div>
  {/if}

  {#if error}<div class="toast">{error}</div>{/if}
</div>

<style>
  .stage { position: fixed; inset: 0; overflow: hidden; }
  canvas { position: absolute; inset: 0; width: 100%; height: 100%; display: block; }
  .overlay { position: absolute; inset: 0; pointer-events: none; overflow: hidden; }
  header { position: absolute; top: 0; left: 0; right: 0; display: flex; justify-content: space-between; align-items: flex-start; padding: 14px 18px; pointer-events: none; }
  header > * { pointer-events: auto; }
  .back { color: var(--dim); text-decoration: none; font-size: 13px; padding: 6px 0; }
  .code { background: var(--panel); border: 1px solid var(--line); padding: 6px 14px; letter-spacing: .3em; font-size: 14px; }
  .score { position: absolute; left: 50%; top: 10px; transform: translateX(-50%); display: flex; align-items: center; gap: 18px; background: var(--panel);
    border: 1px solid var(--line); padding: 6px 26px; backdrop-filter: blur(6px); clip-path: polygon(14px 0, calc(100% - 14px) 0, 100% 100%, 0 100%); }
  .score span { font-size: 44px; line-height: 1; min-width: 36px; text-align: center; }
  .t0 { color: var(--t0); } .t1 { color: var(--t1); }
  .mid { text-align: center; display: grid; }
  .mid b { font-size: 22px; } .mid small { color: var(--dim); font-size: 11px; text-transform: uppercase; letter-spacing: .1em; }

  .team { position: absolute; top: 84px; display: grid; gap: 10px; width: 240px; align-content: start; }
  .team0 { left: 14px; } .team1 { right: 14px; }
  .card { background: var(--panel); border-left: 3px solid var(--team); padding: 10px 12px; display: grid; gap: 7px; backdrop-filter: blur(6px); }
  .team1 .card { border-left: 0; border-right: 3px solid var(--team); }
  .row { display: flex; justify-content: space-between; align-items: center; } .row b { font-size: 15px; }
  .ctl { font-size: 11px; color: var(--dim); text-transform: uppercase; letter-spacing: .08em; }
  .yc { color: var(--gold); font-size: 10px; letter-spacing: 2px; }
  .tps { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; background: #0a1120; border: 1px solid var(--line); padding: 4px 8px; }
  .tps .num { color: var(--team); font-size: 17px; } .tps small { font-size: 10px; text-align: right; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 110px; }
  .chips { display: flex; gap: 4px; flex-wrap: wrap; min-height: 0; }
  .chips em { font: 700 10px var(--display); letter-spacing: .08em; text-transform: uppercase; background: #1d2a44; padding: 2px 6px; font-style: normal; }
  .chips em.warn { background: #5a3b00; color: var(--gold); }
  .skills { display: grid; gap: 3px; }
  .sk { position: relative; height: 20px; background: #0a1120; border: 1px solid var(--line); overflow: hidden; font-size: 11px; }
  .sk span { position: relative; z-index: 1; padding: 0 6px; line-height: 18px; text-shadow: 0 1px 2px #000; }
  .sk i { position: absolute; inset: 0 auto 0 0; background: #ffffff22; }
  .sk.empty { opacity: .4; }

  .feed { position: absolute; left: 14px; bottom: 14px; margin: 0; padding: 0; list-style: none; display: grid; gap: 3px; max-width: 460px; pointer-events: none; }
  .feed li { background: #0b1222cc; padding: 5px 10px; font-size: 12px; border-left: 2px solid var(--dim); }
  .feed li.goal { border-color: var(--gold); color: var(--gold); font-weight: 700; }
  .feed li.forge { border-color: #c06bff; } .feed li.cast { border-color: var(--t0); } .feed li.strike { border-color: var(--t1); color: var(--t1); }

  .banner { position: absolute; left: 50%; top: 36%; transform: translate(-50%, -50%); font-size: clamp(60px, 12vw, 160px); color: var(--gold);
    text-shadow: 0 0 40px #ffd23f88; animation: pop .45s cubic-bezier(.2, 1.6, .4, 1); pointer-events: none; }
  @keyframes pop { from { transform: translate(-50%, -50%) scale(.3); opacity: 0; } }

  .lobby { position: absolute; left: 14px; right: 14px; top: 84px; bottom: 14px; display: flex; gap: 18px; justify-content: space-between; align-items: flex-start; pointer-events: none; }
  .lobby > :global(*) { pointer-events: auto; }
  .invite { max-width: min(430px, 92vw); }
  .benchbtn { position: absolute; right: 14px; bottom: 14px; background: var(--panel); color: var(--dim); border: 1px solid var(--line); padding: 7px 12px; font-size: 11px; }
  .benchpanel { position: absolute; right: 14px; bottom: 54px; width: min(640px, 94vw); background: #0b1222f2; border: 1px solid var(--line); padding: 12px; }
  .benchfinal { width: min(620px, 88vw); text-align: left; }
  .final, .invite { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); background: #0b1222ee; border: 1px solid var(--line);
    padding: 28px 36px; text-align: center; backdrop-filter: blur(10px); display: grid; gap: 10px; justify-items: center; max-width: min(560px, 92vw); }
  .invite { position: static; transform: none; padding: 20px 24px; }
  .big { font-size: 52px !important; }
  .final h2 { font-size: 90px; margin: 0; line-height: 1; } .final p { margin: 0; } .win { font-size: 24px; }
  .again, .go, .alt { padding: 12px 22px; text-decoration: none; border: 0; font-size: 15px; }
  .again, .go { background: var(--gold); color: #1a1300; } .go:disabled { opacity: .4; cursor: not-allowed; }
  .alt { background: transparent; color: var(--t0); border: 1px solid var(--t0); }
  .kick { color: var(--gold); margin: 0; font-size: 13px; }
  .big { font-size: 68px; letter-spacing: .18em; color: var(--t0); text-shadow: 0 0 30px #35e0ff66; margin-right: -.18em; }
  .seats { margin: 0; color: var(--dim); }
  .seatlist { list-style: none; display: flex; gap: 6px; padding: 0; margin: 0; flex-wrap: wrap; justify-content: center; }
  .seatlist li { border-bottom: 2px solid; padding: 4px 10px; background: #0a1120; font-size: 13px; } .seatlist li.free { border-color: var(--line); color: var(--dim); }
  pre { text-align: left; background: #0a1120; padding: 10px 12px; font-size: 12px; color: var(--dim); margin: 4px 0; white-space: pre-wrap; border: 1px solid var(--line); width: 100%; }
  .btns { display: flex; gap: 10px; } small { color: var(--dim); }
  .toast { position: absolute; bottom: 14px; right: 14px; background: var(--t1); padding: 8px 14px; }
  @media (max-width: 900px) { .team { width: 170px; } .skills { display: none; } }
</style>
