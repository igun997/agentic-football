<script lang="ts">
  import Lobby from './components/Lobby.svelte'
  import Match from './components/Match.svelte'

  let hash = $state(location.hash)
  $effect(() => {
    const on = () => (hash = location.hash)
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  })
  const code = $derived(/^#\/room\/([A-Za-z0-9]+)/.exec(hash)?.[1]?.toUpperCase() ?? '')
</script>

{#if code}
  {#key code}<Match {code} />{/key}
{:else}
  <Lobby />
{/if}
