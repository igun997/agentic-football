<script lang="ts">
  import { saveBrief } from '../lib/api'
  import { TEAM_COLORS } from '../lib/game/arena'
  import type { Brief, RoomInfo, SkillBrief, SkillInfo } from '../lib/types'

  let { info, editable, onSaved, onPreview }: {
    info: RoomInfo
    editable: boolean
    onSaved: (i: RoomInfo) => void
    onPreview: (s: SkillInfo) => void
  } = $props()

  const STYLES = [['balanced', 'Balanced'], ['aggressive', 'Aggressive'], ['defensive', 'Defensive'], ['trickster', 'Trickster']]
  const ROLES = ['FWD', 'MID', 'DEF', 'GK']
  const ARCH: Record<string, string> = {
    attack: 'Hits the ball: impulse, blast, projectile, dash',
    defense: 'Walls, shields, blasts to clear danger',
    control: 'Freeze/slow enemies, magnet, walls, projectiles',
    mobility: 'Dashes and haste for yourself',
    trick: 'Anything goes — a mix of all effects',
  }
  const ELEMENTS: Record<string, string> = { fire: '#ff6a2b', ice: '#66ddff', storm: '#ffe14a', void: '#b26bff', nature: '#6bff8a', cosmic: '#ff6bd6', custom: '#ffffff' }
  const INTENSITY = [['low', 'Low · 1.2s'], ['medium', 'Medium · 2s'], ['high', 'High · 3s']]

  const key = (t: number, i: number) => `${t}-${i}`
  // slots are fixed for the life of a room: seed one editable draft per slot at mount; edits stay local until saved
  // svelte-ignore state_referenced_locally
  let drafts = $state<Record<string, Brief>>(Object.fromEntries(info.slots.map((s) => [key(s.team, s.index), { ...structuredClone($state.snapshot(s.brief)), skills: structuredClone($state.snapshot(s.brief.skills ?? [])) } as Brief])))
  let open = $state('0-0')
  let busy = $state('')
  let msg = $state<Record<string, string>>({})

  const blank = (slot: number): SkillBrief => ({ slot, concept: '', archetype: 'attack', element: 'fire', color: ELEMENTS.fire, intensity: 'medium', power_budget: 25 })
  const draftSkill = (b: Brief, slot: number) => b.skills.find((s) => s.slot === slot)
  function toggle(b: Brief, slot: number) {
    const i = b.skills.findIndex((s) => s.slot === slot)
    if (i >= 0) b.skills.splice(i, 1)
    else b.skills.push(blank(slot))
  }
  function setElement(s: SkillBrief, el: string) {
    s.element = el
    if (el !== 'custom') s.color = ELEMENTS[el]
  }
  async function save(t: number, i: number) {
    const k = key(t, i)
    busy = k; msg[k] = ''
    try {
      const out = await saveBrief(info.code, t, i, $state.snapshot(drafts[k]) as Brief)
      drafts[k] = structuredClone(out.slots.find((s) => s.team === t && s.index === i)!.brief)
      msg[k] = 'Saved ✓'
      onSaved(out)
    } catch (e) { msg[k] = (e as Error).message } finally { busy = '' }
  }
  const forged = (agent?: string) => info.skills.filter((s) => s.owner === agent).sort((a, b) => a.slot - b.slot)
</script>

<section class="trainer">
  <header>
    <h3 class="display">Trainer setup</h3>
    <p>Build your agent before kickoff. Your brief becomes the guardrails the MCP enforces when the agent forges its skills.</p>
  </header>

  {#each info.slots as slot (key(slot.team, slot.index))}
    {@const k = key(slot.team, slot.index)}
    {@const d = drafts[k]}
    <article style:--team={TEAM_COLORS[slot.team]} class:open={open === k}>
      <button class="head" onclick={() => (open = open === k ? '' : k)}>
        <span class="dot"></span>
        <b class="display">{slot.team === 0 ? 'Cyan' : 'Red'} · Agent {slot.index + 1}</b>
        <span class="state">
          {#if slot.agent_id}{slot.name} · {slot.ready ? 'ready ✓' : 'forging…'}{:else}waiting for an agent{/if}
        </span>
        <span class="chev">{open === k ? '−' : '+'}</span>
      </button>

      {#if open === k && d}
        <div class="body">
          <div class="row"><span class="lbl">Play style</span>
            <div class="chips">{#each STYLES as [v, n]}<button class:on={d.persona.style === v} disabled={!editable} onclick={() => (d.persona.style = v)}>{n}</button>{/each}</div></div>
          <div class="row"><span class="lbl">Starts as</span>
            <div class="chips">{#each ROLES as r}<button class:on={d.persona.preferred_role === r} disabled={!editable} onclick={() => (d.persona.preferred_role = r)}>{r}</button>{/each}</div></div>
          <div class="row"><span class="lbl">Motto</span>
            <input maxlength="60" placeholder="what your agent shouts" bind:value={d.persona.motto} disabled={!editable} /></div>
          <div class="row"><span class="lbl">Live forging</span>
            <div class="chips"><button class:on={!d.lock_loadout} disabled={!editable} onclick={() => (d.lock_loadout = false)}>Agent may forge in-match</button>
              <button class:on={d.lock_loadout} disabled={!editable} onclick={() => (d.lock_loadout = true)}>Locked loadout</button></div></div>

          <h4 class="display">Skill moves</h4>
          {#each [0, 1, 2] as sl}
            {@const sb = draftSkill(d, sl)}
            <div class="skill" class:off={!sb}>
              <div class="skh">
                <b class="display">Slot {sl + 1}</b>
                <button class="sw" disabled={!editable} onclick={() => toggle(d, sl)}>{sb ? 'remove' : '+ brief a skill'}</button>
              </div>
              {#if sb}
                <textarea rows="2" maxlength="140" placeholder="Describe the skill: e.g. “a comet that freezes defenders and leaves a trail of ice”" bind:value={sb.concept} disabled={!editable}></textarea>
                <div class="chips">{#each Object.keys(ARCH) as a}<button class:on={sb.archetype === a} disabled={!editable} title={ARCH[a]} onclick={() => (sb.archetype = a)}>{a}</button>{/each}</div>
                <small class="hint">{ARCH[sb.archetype]}</small>
                <div class="chips el">{#each Object.keys(ELEMENTS) as e}
                  <button class:on={sb.element === e} disabled={!editable} onclick={() => setElement(sb, e)}><i style:background={e === 'custom' ? sb.color : ELEMENTS[e]}></i>{e}</button>{/each}
                  {#if sb.element === 'custom'}<input type="color" bind:value={sb.color} disabled={!editable} />{/if}</div>
                <div class="chips">{#each INTENSITY as [v, n]}<button class:on={sb.intensity === v} disabled={!editable} onclick={() => (sb.intensity = v)}>{n}</button>{/each}</div>
                <label class="rng">Power budget <b>{sb.power_budget}</b> / 50 <input type="range" min="8" max="50" step="1" bind:value={sb.power_budget} disabled={!editable} /></label>
              {/if}
            </div>
          {/each}

          {#if slot.agent_id}
            <h4 class="display">Forged by {slot.name}</h4>
            {#each forged(slot.agent_id) as sk}
              <div class="forged"><span><b>{sk.spec.name}</b> <small>slot {sk.slot + 1} · power {sk.power} · cd {sk.cooldown}s</small></span>
                <button onclick={() => onPreview(sk)}>▶ Preview</button></div>
            {:else}<p class="none">Nothing forged yet.</p>{/each}
          {/if}

          {#if editable}
            <div class="save"><button class="go display" disabled={busy === k} onclick={() => save(slot.team, slot.index)}>Save setup</button>
              {#if msg[k]}<span>{msg[k]}</span>{/if}</div>
          {:else}<p class="none">Only the room host can edit the setup.</p>{/if}
        </div>
      {/if}
    </article>
  {/each}
</section>

<style>
  .trainer { display: grid; gap: 8px; width: min(460px, 94vw); max-height: calc(100vh - 120px); overflow: auto; padding-right: 4px; }
  header h3 { margin: 0 0 4px; color: var(--gold); font-size: 14px; } header p { margin: 0; color: var(--dim); font-size: 12px; }
  article { background: #0b1222ee; border-left: 3px solid var(--team); }
  .head { all: unset; box-sizing: border-box; width: 100%; display: flex; gap: 10px; align-items: center; padding: 10px 12px; cursor: pointer; }
  .dot { width: 9px; height: 9px; background: var(--team); } .state { color: var(--dim); font-size: 12px; margin-left: auto; } .chev { color: var(--dim); }
  .body { padding: 4px 12px 14px; display: grid; gap: 10px; }
  .row { display: grid; gap: 5px; } .lbl, label { color: var(--dim); font-size: 11px; text-transform: uppercase; letter-spacing: .07em; }
  h4 { margin: 8px 0 0; font-size: 12px; color: var(--t0); }
  .chips { display: flex; gap: 5px; flex-wrap: wrap; align-items: center; }
  .chips button, .forged button, .sw { background: #0a1120; color: var(--text); border: 1px solid var(--line); padding: 5px 9px; font-size: 12px; text-transform: capitalize; }
  .chips button.on { background: var(--team); color: #04121a; border-color: var(--team); font-weight: 700; }
  .chips button:disabled { opacity: .6; cursor: default; }
  .el i { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 5px; }
  input:not([type]), textarea { background: #0a1120; border: 1px solid var(--line); color: var(--text); padding: 7px 9px; font: inherit; font-size: 13px; width: 100%; resize: vertical; }
  .skill { border: 1px solid var(--line); padding: 9px; display: grid; gap: 7px; background: #09101e; } .skill.off { opacity: .7; }
  .skh { display: flex; justify-content: space-between; align-items: center; } .skh b { font-size: 12px; }
  .hint { color: var(--dim); font-size: 11px; margin-top: -3px; }
  .rng { display: flex; align-items: center; gap: 8px; text-transform: none; letter-spacing: 0; } .rng input { flex: 1; } .rng b { color: var(--gold); }
  .forged { display: flex; justify-content: space-between; align-items: center; background: #0a1120; padding: 6px 9px; border-left: 2px solid #c06bff; } small { color: var(--dim); }
  .none { color: var(--dim); font-size: 12px; margin: 0; }
  .save { display: flex; gap: 12px; align-items: center; } .save span { color: var(--ok); font-size: 12px; }
  .go { background: var(--gold); color: #1a1300; border: 0; padding: 10px 18px; }
</style>
