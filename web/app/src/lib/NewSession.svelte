<script lang="ts">
  import { api, type Dirs } from './api'

  let { stack }: { stack: string } = $props()

  let listing = $state<Dirs | undefined>()
  let cwd = $state('') // picked: the second step
  let agents = $state<string[]>([])
  let agent = $state(localStorage.getItem('stackk.agent') || '')
  api.agents().then((a) => {
    agents = a
    if (!a.includes(agent)) agent = a[0] ?? ''
  }, () => {})
  let prompt = $state('')
  let error = $state('')
  let busy = $state(false)

  async function open(path = '') {
    try {
      listing = await api.dirs(path)
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }
  open()

  // ~/Work/stackk instead of /Users/you/Work/stackk
  let home = $state('')
  $effect(() => {
    if (listing && !listing.parent) home = listing.path
  })
  const short = (p: string) => (home && p.startsWith(home) ? '~' + p.slice(home.length) : p)

  async function start() {
    busy = true
    try {
      localStorage.setItem('stackk.agent', agent)
      const { id } = await api.newTask({ cwd, prompt, agent, stack })
      location.hash = `#/t/${encodeURIComponent(id)}`
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }
</script>

<div class="page">
  <header>
    {#if cwd}
      <button class="back" aria-label="back" onclick={() => (cwd = '')}>‹</button>
    {:else}
      <a class="back" href="#/" aria-label="back">‹</a>
    {/if}
    <span class="title">new session</span>
  </header>

  {#if !cwd}
    <div class="scroll">
      {#if listing?.recent?.length}
        <h3>recent</h3>
        {#each listing.recent as d (d.path)}
          <button class="row" onclick={() => (cwd = d.path)}>
            <span class="name">{d.name}</span><span class="path">{short(d.path)}</span>
          </button>
        {/each}
      {/if}

      <h3>{listing ? short(listing.path) : '…'}</h3>
      {#if listing?.parent}
        <button class="row" onclick={() => open(listing!.parent)}><span class="name dim">‹ up</span></button>
      {/if}
      {#each listing?.dirs ?? [] as d (d.path)}
        <span class="folder">
          <button class="row grow" onclick={() => open(d.path)}>
            <span class="name">{d.name}</span>{#if d.git}<span class="git">git</span>{/if}
          </button>
          <button class="pick" onclick={() => (cwd = d.path)}>use</button>
        </span>
      {:else}
        {#if listing}<p class="dim pad">no folders here</p>{/if}
      {/each}
      {#if error}<p class="error pad">{error}</p>{/if}
    </div>
    {#if listing?.parent}
      <div class="dock">
        <button class="primary" onclick={() => (cwd = listing!.path)}>use {short(listing.path)}</button>
      </div>
    {/if}
  {:else}
    <div class="scroll pad">
      <h3 class="flush">folder</h3>
      <p class="chosen">{short(cwd)}</p>
      <h3 class="flush">agent</h3>
      <div class="seg">
        {#each agents as a (a)}
          <button class:on={agent === a} onclick={() => (agent = a)}>{a}</button>
        {/each}
      </div>
      <h3 class="flush">what should it do?</h3>
      <textarea rows="5" placeholder="optional: a first prompt" bind:value={prompt} autocapitalize="sentences"></textarea>
      {#if error}<p class="error">{error}</p>{/if}
    </div>
    <div class="dock">
      <button class="primary" disabled={busy} onclick={start}>{busy ? 'starting…' : 'start'}</button>
    </div>
  {/if}
</div>

<style>
  .page { position: fixed; inset: 0; display: flex; flex-direction: column; }
  header { display: flex; align-items: center; gap: 8px; padding: env(safe-area-inset-top) 16px 0 0; border-bottom: 1px solid var(--line); }
  .back { display: grid; place-items: center; width: var(--tap); height: var(--tap); color: var(--accent); text-decoration: none; font-size: 26px; }
  .title { color: var(--bright); font-weight: 700; }
  .scroll { flex: 1; overflow: auto; padding-bottom: 16px; }
  .pad { padding-left: 16px; padding-right: 16px; }
  h3 { font: inherit; color: var(--dim); margin: 18px 16px 4px; word-break: break-all; }
  h3.flush { margin-left: 0; margin-right: 0; }
  .row { display: flex; align-items: center; gap: 1ch; width: 100%; min-height: 46px; padding: 0 16px; text-align: left; }
  .row:active, .pick:active { background: var(--press); }
  .folder { display: flex; align-items: center; }
  .grow { flex: 1; min-width: 0; }
  .name { color: var(--bright); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .path { color: var(--dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
  .git { color: var(--green); }
  .pick { color: var(--accent); min-height: 46px; padding: 0 16px; }
  .dim { color: var(--dim); }
  .error { color: var(--red); }
  .chosen { color: var(--bright); margin: 0; word-break: break-all; }
  .seg { display: flex; border: 1px solid var(--line); border-radius: 10px; overflow: hidden; }
  .seg button { flex: 1; min-height: var(--tap); color: var(--muted); }
  .seg button.on { background: var(--raised); color: var(--bright); font-weight: 700; }
  textarea {
    width: 100%;
    resize: none;
    font-size: 16px; /* iOS doesn't zoom in at 16px */
    line-height: 1.4;
    color: var(--bright);
    background: var(--raised);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 10px 12px;
  }
  textarea:focus { outline: none; border-color: var(--dim); }
  .dock { border-top: 1px solid var(--line); padding: 8px 12px max(env(safe-area-inset-bottom), 8px); }
  .primary { width: 100%; min-height: 48px; border-radius: 12px; background: var(--accent); color: var(--bg); font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 0 12px; }
  .primary:disabled { opacity: 0.6; }
</style>
