<script lang="ts">
  import { ago, type Task } from './api'
  import { applySize, applyTheme, currentSize, currentTheme, sizes, themes, type SizeID, type ThemeID } from './theme'

  let { tasks, error }: { tasks: Task[]; error: string } = $props()

  let stack = $derived(tasks.filter((t) => t.view === 'stack').sort((a, b) => Number(!!b.priority) - Number(!!a.priority) || a.queued_at - b.queued_at))
  let running = $derived(tasks.filter((t) => t.view === 'running').sort((a, b) => a.phase_since - b.phase_since))
  let scheduled = $derived(tasks.filter((t) => t.view === 'deferred' || t.view === 'exited'))
  let paused = $derived(tasks.filter((t) => t.view === 'paused'))
  let hidden = $derived(tasks.filter((t) => t.view === 'hidden'))
  let stackName = $derived(tasks.find((t) => t.stack_name)?.stack_name ?? '')

  let open = $state<Record<string, boolean>>({})
  let settings = $state(false)
  let theme = $state<ThemeID>(currentTheme())
  let size = $state<SizeID>(currentSize())

  function pickTheme(id: ThemeID) {
    theme = id
    applyTheme(id)
  }
  // What iOS reports about the screen: for layout bugs only it can show.
  function metrics(): string {
    const probe = document.createElement('div')
    probe.style.cssText = 'position:fixed;padding:env(safe-area-inset-top) 0 env(safe-area-inset-bottom)'
    document.body.append(probe)
    const cs = getComputedStyle(probe)
    const safe = `${cs.paddingTop}/${cs.paddingBottom}`
    probe.remove()
    const standalone = (navigator as { standalone?: boolean }).standalone ? 'app' : 'browser'
    return `${standalone} · screen ${screen.width}×${screen.height} · window ${innerWidth}×${innerHeight} · visible ${Math.round(visualViewport?.height ?? 0)} · safe ${safe}`
  }

  function pickSize(id: SizeID) {
    size = id
    applySize(id)
  }

  // A tick so the ages move between polls.
  let now = $state(Date.now())
  $effect(() => {
    const id = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(id)
  })

  function status(t: Task): string {
    if (t.run_state === 'exited') return 'done'
    if (t.phase === 'waiting') return t.waiting_kind === 'question' ? 'asks' : 'permission'
    if (t.phase === 'working') return 'working'
    if (t.phase === 'shell') return 'shell'
    if (t.phase === 'unknown') return 'quiet'
    return 'replied'
  }
</script>

{#snippet row(t: Task)}
  <a class="task" class:waiting={t.phase === 'waiting'} href="#/t/{encodeURIComponent(t.id)}">
    <span class="top">
      <span class="name">{#if t.priority}<span class="prio">★</span> {/if}{t.name}</span>
      <span class="age">{ago(t.view === 'running' ? t.phase_since : t.queued_at || t.phase_since, now)}</span>
    </span>
    <span class="sub">
      <span class="status">{status(t)}</span>
      {#if t.error}<span class="err">{t.error}</span>
      {:else if t.view === 'running' && t.last_tool}{t.last_tool}
      {:else}{(t.last_msg || t.note || '').replace(/^(permission|question): /, '')}{/if}
    </span>
  </a>
{/snippet}

{#snippet folded(key: string, title: string, list: Task[])}
  {#if list.length}
    <button class="fold" onclick={() => (open[key] = !open[key])}>
      {open[key] ? '▾' : '▸'} {title} <span class="n">{list.length}</span>
    </button>
    {#if open[key]}{#each list as t (t.id)}{@render row(t)}{/each}{/if}
  {/if}
{/snippet}

<header>
  <span class="brand">stackk</span>
  <span class="stackname">{stackName}</span>
  <a class="new" href="#/new">+ new</a>
  <button class="menu-btn" aria-label="settings" onclick={() => (settings = true)}>⚙</button>
</header>

<main>
  {#if error}<p class="error">{error}</p>{/if}

  <h2 class="stack">waiting for you <span class="n">{stack.length}</span></h2>
  {#each stack as t (t.id)}
    {@render row(t)}
  {:else}
    <p class="empty">nothing — go ride</p>
  {/each}

  <h2>running <span class="n">{running.length}</span></h2>
  {#each running as t (t.id)}{@render row(t)}{/each}

  {@render folded('scheduled', 'scheduled', scheduled)}
  {@render folded('paused', 'paused', paused)}
  {@render folded('hidden', 'hidden', hidden)}
</main>

{#if settings}
  <button class="scrim" aria-label="close" onclick={() => (settings = false)}></button>
  <div class="sheet">
    <div class="sheet-head">
      <span>settings</span>
      <button class="close" onclick={() => (settings = false)}>done</button>
    </div>
    <div class="sheet-list">
      <h3>theme</h3>
      {#each themes as t (t.id)}
        <button class="opt-row" class:on={theme === t.id} onclick={() => pickTheme(t.id)}>
          <span class="dot">{theme === t.id ? '●' : '○'}</span> {t.label}
        </button>
      {/each}
      <h3>text size</h3>
      <div class="sizes">
        {#each sizes as z (z.id)}
          <button class:on={size === z.id} onclick={() => pickSize(z.id)}>{z.label}</button>
        {/each}
      </div>
      <h3>connected to</h3>
      <p class="host">{location.host}</p>
      <h3>screen</h3>
      <p class="host">{metrics()}</p>
    </div>
  </div>
{/if}

<style>
  header {
    position: sticky;
    top: 0;
    z-index: 1;
    display: flex;
    align-items: baseline;
    gap: 1ch;
    padding: calc(env(safe-area-inset-top) + 12px) 16px 12px;
    background: var(--bg);
    border-bottom: 1px solid var(--line);
  }
  .brand { color: var(--accent); font-weight: 700; }
  .stackname { color: var(--dim); flex: 1; }
  .new { color: var(--accent); text-decoration: none; padding: 10px 4px; margin: -10px 0; }
  .menu-btn { color: var(--muted); min-height: var(--tap); padding: 0 4px 0 12px; margin: -12px 0; }
  .scrim { position: fixed; inset: 0; z-index: 2; background: rgb(0 0 0 / 0.5); }
  .sheet {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 3;
    max-height: 75dvh;
    display: flex;
    flex-direction: column;
    background: var(--raised);
    border-top: 1px solid var(--line);
    border-radius: 14px 14px 0 0;
    padding-bottom: env(safe-area-inset-bottom);
  }
  .sheet-head { display: flex; align-items: center; justify-content: space-between; padding: 6px 4px 6px 16px; font-weight: 700; color: var(--muted); }
  .close { color: var(--accent); min-height: var(--tap); padding: 0 12px; font-weight: 400; }
  .sheet-list { overflow: auto; }
  h3 { font: inherit; color: var(--dim); margin: 14px 16px 4px; }
  .opt-row { display: block; width: 100%; text-align: left; min-height: 46px; padding: 0 16px; }
  .opt-row:active { background: var(--press); }
  .opt-row .dot { color: var(--dim); margin-right: 1ch; }
  .opt-row.on { color: var(--bright); }
  .opt-row.on .dot { color: var(--accent); }
  .sizes { display: flex; gap: 8px; padding: 4px 16px 8px; }
  .sizes button { flex: 1; min-height: 44px; border: 1px solid var(--line); border-radius: 10px; color: var(--muted); }
  .sizes button.on { border-color: var(--accent); color: var(--bright); }
  .host { margin: 0 16px 20px; color: var(--muted); word-break: break-all; }
  main { padding: 4px 0 calc(env(safe-area-inset-bottom) + 32px); max-width: 720px; margin: 0 auto; }
  h2, .fold {
    display: block;
    width: 100%;
    text-align: left;
    font: inherit;
    font-weight: 700;
    color: var(--muted);
    margin: 0;
    padding: 20px 16px 8px;
  }
  h2.stack { color: var(--accent); }
  .fold { min-height: var(--tap); }
  .n { color: var(--dim); font-weight: 400; }
  .task {
    display: block;
    text-decoration: none;
    color: inherit;
    padding: 12px 16px;
    border-left: 3px solid transparent;
  }
  .task:active { background: var(--press); }
  .task.waiting { border-left-color: var(--accent); background: var(--raised); }
  .top { display: flex; gap: 1ch; }
  .name { color: var(--bright); font-weight: 700; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .prio { color: var(--accent); }
  .age { color: var(--dim); }
  .sub { display: block; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status { color: var(--dim); margin-right: 1ch; }
  .waiting .status { color: var(--accent); }
  .err { color: var(--red); }
  .empty { color: var(--dim); padding: 4px 16px; margin: 0; }
  .error { color: var(--red); padding: 8px 16px; margin: 0; }
</style>
