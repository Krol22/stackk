<script lang="ts">
  import { ago, mark, type Task } from './api'

  let { tasks, error }: { tasks: Task[]; error: string } = $props()

  let stack = $derived(tasks.filter((t) => t.view === 'stack').sort((a, b) => a.queued_at - b.queued_at))
  let running = $derived(tasks.filter((t) => t.view === 'running').sort((a, b) => a.phase_since - b.phase_since))
  let scheduled = $derived(tasks.filter((t) => t.view === 'deferred' || t.view === 'exited'))
  let paused = $derived(tasks.filter((t) => t.view === 'paused'))
  let waiting = $derived(stack.filter((t) => t.phase === 'waiting').length)

  // A tick so the ages and the clock move between polls.
  let now = $state(Date.now())
  $effect(() => {
    const id = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(id)
  })
  let clock = $derived(new Date(now).toTimeString().slice(0, 5))
</script>

{#snippet section(title: string, note: string, cls: string)}
  <div class="rule {cls}"><span>──</span><span class="t">{title}</span><span class="line"></span>{#if note}<span>{note}</span><span>──</span>{/if}</div>
{/snippet}

{#snippet row(t: Task, n: string, kind: string)}
  <a class="task {kind}" class:waiting={t.phase === 'waiting'} href="#/t/{encodeURIComponent(t.id)}">
    <span class="nr">{n}</span>
    <span class="body">
      <span class="top">
        <span class="name">{t.name}</span>
        <span class="agent {t.agent || 'shell'}">{t.agent || 'sh'}</span>
        <span class="mark">{mark(t)}</span>
        <span class="age">{ago(kind === 'running' ? t.phase_since : t.queued_at || t.phase_since, now)}</span>
      </span>
      {#if t.error}
        <span class="msg err">E: {t.error}</span>
      {:else if kind === 'running' && t.last_tool}
        <span class="msg">» {t.last_tool}</span>
      {:else if t.last_msg}
        <span class="msg">{t.last_msg}</span>
      {:else if t.note}
        <span class="msg">" {t.note}</span>
      {/if}
    </span>
  </a>
{/snippet}

<div class="screen">
  <main>
    <header>
      <span class="brand">stackk</span><span class="sep">:</span><span class="stackname">{stack[0]?.stack_name ?? running[0]?.stack_name ?? ''}</span>
      <span class="counts"><b class="s">●{stack.length}</b> <b class="r">◐{running.length}</b> <b class="z">z{scheduled.length}</b></span>
    </header>

    {#if error}<p class="error">E: {error}</p>{/if}

    {@render section('STACK', 'oldest first', 'stack')}
    {#each stack as t, i (t.id)}
      {@render row(t, String(i + 1), 'stack')}
    {:else}
      <p class="empty">-- nothing is waiting for you --</p>
    {/each}

    {@render section('RUNNING', 'do not disturb', 'running')}
    {#each running as t (t.id)}
      {@render row(t, '◐', 'running')}
    {:else}
      <p class="empty tilde">~</p>
    {/each}

    {#if scheduled.length}
      {@render section('SCHEDULED', '', 'sched')}
      {#each scheduled as t (t.id)}
        {@render row(t, 'z', 'scheduled')}
      {/each}
    {/if}

    {#if paused.length}
      {@render section('PAUSED', '', 'paused')}
      {#each paused as t (t.id)}
        {@render row(t, '‖', 'paused')}
      {/each}
    {/if}

    {#each Array(6) as _}<div class="tilde">~</div>{/each}
  </main>

  <footer class="statusline">
    <span class="mode" class:wait={waiting > 0}>{waiting ? 'WAIT' : 'NORMAL'}</span>
    <span class="seg">stack</span>
    <span class="fill">{stack.length} waiting · {running.length} running</span>
    <span>{clock}</span>
  </footer>
</div>

<style>
  /* one size, one font: the whole list is a terminal */
  .screen { min-height: 100dvh; display: flex; flex-direction: column; font: 14px/1.45 var(--mono); }
  .statusline { font-size: inherit; }
  main {
    flex: 1;
    padding: calc(env(safe-area-inset-top) + 10px) 10px 10px;
    max-width: 760px;
    width: 100%;
    margin: 0 auto;
  }
  footer { position: sticky; bottom: 0; padding-bottom: env(safe-area-inset-bottom); }
  header { display: flex; align-items: baseline; padding: 2px 0 12px; }
  .brand { color: var(--accent); font-weight: 700; }
  .sep { color: var(--dim); }
  .stackname { color: var(--muted); margin-right: auto; }
  .counts b { font-weight: 500; }
  .counts .s { color: var(--accent); }
  .counts .r { color: var(--blue); }
  .counts .z { color: var(--purple); }
  .rule { margin: 14px 0 4px; }
  .rule.stack .t { color: var(--accent); }
  .rule.running .t { color: var(--blue); }
  .rule.sched .t { color: var(--purple); }
  .rule.paused .t { color: var(--dim); }
  .task {
    display: flex;
    gap: 1ch;
    text-decoration: none;
    color: inherit;
    padding: 5px 4px 5px 0;
  }
  .task:active, .task:focus-visible { background: var(--cursor); outline: none; }
  .nr { width: 3ch; text-align: right; color: var(--dim); flex: none; }
  .stack .nr { color: var(--accent); }
  .waiting .nr { color: var(--bg); background: var(--accent); }
  .body { flex: 1; min-width: 0; }
  .top { display: flex; gap: 1ch; }
  .name { color: var(--bright); flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .agent { color: var(--green); }
  .agent.omp { color: var(--purple); }
  .agent.shell { color: var(--muted); }
  .mark { color: var(--muted); }
  .waiting .mark { color: var(--accent); }
  .age { color: var(--dim); min-width: 3ch; text-align: right; }
  .msg {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    color: var(--muted);
  }
  .msg.err { color: var(--red); }
  .empty { color: var(--dim); margin: 4px 0 4px 4ch; }
  .error { color: var(--red); margin: 0 0 8px; }
  .tilde { color: var(--dim); padding-left: 1ch; }
</style>
