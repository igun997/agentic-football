<script lang="ts">
  import { TEAM_COLORS } from '../lib/game/arena'
  import type { AgentView } from '../lib/types'

  let { agents, compact = false }: { agents: AgentView[]; compact?: boolean } = $props()

  const rows = $derived(
    [...agents]
      .filter((a) => a.bench)
      .sort((a, b) => (b.bench!.tokens_per_sec - a.bench!.tokens_per_sec) || (b.bench!.calls_avg - a.bench!.calls_avg)),
  )
  const ms = (v: number) => (v ? (v >= 1000 ? (v / 1000).toFixed(1) + 's' : Math.round(v) + 'ms') : '—')
</script>

<table class:compact>
  <thead>
    <tr><th></th><th>Agent</th><th title="self-reported: output tokens / generation time">TPS tok/s*</th><th title="tool calls per second, measured (rolling 10s)">calls/s</th>
      <th title="gap between a reply and the agent's next call">turn p50</th><th title="observe → action">reaction</th><th title="time to author a skill">forge</th><th>err</th></tr>
  </thead>
  <tbody>
    {#each rows as a, i (a.id)}
      {@const b = a.bench!}
      <tr style:--team={TEAM_COLORS[a.team]}>
        <td class="rk">{i + 1}</td>
        <td><b>{a.name}</b><small>{b.provider || '—'}{b.model ? ' · ' + b.model : ''}</small></td>
        <td class="big">{b.tokens_per_sec ? b.tokens_per_sec.toFixed(1) : '—'}</td>
        <td class="big">{b.calls_per_sec.toFixed(1)}<small>avg {b.calls_avg.toFixed(1)}</small></td>
        <td>{ms(b.turn_p50_ms)}<small>p95 {ms(b.turn_p95_ms)}</small></td>
        <td>{ms(b.reaction_p50_ms)}</td>
        <td>{ms(b.author_avg_ms)}</td>
        <td>{b.errors}<small>/{b.calls}</small></td>
      </tr>
    {:else}
      <tr><td colspan="8" class="none">no agent has called the MCP yet</td></tr>
    {/each}
  </tbody>
</table>
<p class="foot">* TPS tokens/s is self-reported by the agent (report_usage). calls/s, turn, reaction and forge time are measured by the server. Nothing here limits the agents.</p>

<style>
  table { border-collapse: collapse; width: 100%; font-size: 13px; }
  th { text-align: left; font: 700 10px var(--display); letter-spacing: .1em; text-transform: uppercase; color: var(--dim); padding: 4px 8px; border-bottom: 1px solid var(--line); }
  td { padding: 6px 8px; border-bottom: 1px solid #14203a; vertical-align: top; }
  tr[style] td:nth-child(2) { border-left: 3px solid var(--team); }
  td b { display: block; } small { display: block; color: var(--dim); font-size: 10px; }
  .big { font: 800 18px var(--display); }
  .rk { color: var(--gold); font: 800 16px var(--display); width: 24px; }
  .none { color: var(--dim); text-align: center; padding: 14px; }
  .foot { color: var(--dim); font-size: 10px; margin: 8px 0 0; max-width: 60ch; }
  .compact td, .compact th { padding: 3px 6px; } .compact .big { font-size: 14px; }
</style>
