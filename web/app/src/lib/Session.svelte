<script lang="ts">
  import { onDestroy, tick } from 'svelte'
  import { api, mark, poll, type Task } from './api'

  let { id, task }: { id: string; task?: Task } = $props()

  let lines = $state<string[]>([])
  let error = $state('')
  let text = $state('')
  let busy = $state(false)
  let typing = $state(false)
  let wrap = $state(localStorage.getItem('stackk.wrap') !== '0')
  let screenEl: HTMLElement | undefined = $state()
  let stuck = true // follow the bottom until you scroll up

  async function refresh() {
    try {
      const s = await api.screen(id)
      lines = s.lines
      error = ''
      if (stuck) {
        await tick()
        screenEl?.scrollTo({ top: screenEl.scrollHeight })
      }
    } catch (e) {
      error = (e as Error).message
    }
  }

  const stop = poll(refresh, 1000)
  onDestroy(stop)

  function onScroll() {
    if (!screenEl) return
    stuck = screenEl.scrollHeight - screenEl.scrollTop - screenEl.clientHeight < 40
  }

  async function act(fn: () => Promise<unknown>) {
    busy = true
    try {
      await fn()
      error = ''
      stuck = true
      setTimeout(refresh, 250)
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  function send() {
    const t = text.trim()
    if (!t) return
    act(async () => {
      await api.send(id, t)
      text = ''
    })
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  function toggleWrap() {
    wrap = !wrap
    localStorage.setItem('stackk.wrap', wrap ? '1' : '0')
  }

  const keys: [string, string][] = [
    ['1', '1'], ['2', '2'], ['3', '3'], ['y', 'y'], ['n', 'n'], ['esc', 'esc'], ['enter', 'CR'],
    ['up', '↑'], ['down', '↓'], ['tab', 'tab'], ['shift-tab', 's-tab'], ['ctrl-c', '^C'],
  ]
  let waiting = $derived(task?.phase === 'waiting')
</script>

<div class="session">
  <header class="tabline">
    <a class="back" href="#/">:q</a>
    <span class="tab">{task?.name ?? id}</span>
    <span class="fill"></span>
    <button onclick={toggleWrap}>[{wrap ? 'wrap' : 'nowrap'}]</button>
    {#if task?.view === 'paused' || task?.run_state === 'exited'}
      <button disabled={busy} onclick={() => act(() => api.action(id, 'resume'))}>[resume]</button>
    {:else if task}
      <button disabled={busy} onclick={() => act(() => api.action(id, 'pause'))}>[pause]</button>
    {/if}
  </header>

  <pre class="term" class:wrap bind:this={screenEl} onscroll={onScroll}>{lines.join('\n')}</pre>

  <div class="keys" class:waiting>
    {#each keys as [k, label] (k)}
      <button disabled={busy} onclick={() => act(() => api.keys(id, [k]))}>[{label}]</button>
    {/each}
  </div>

  <footer class="statusline">
    <span class="mode" class:insert={typing} class:wait={!typing && waiting}>{typing ? 'INSERT' : waiting ? 'WAIT' : 'NORMAL'}</span>
    <span class="seg">{task?.agent || 'sh'}</span>
    <span class="fill">{#if task}{mark(task)} {task.view} · {task.phase}{task.waiting_kind ? ` (${task.waiting_kind})` : ''}{/if}</span>
    <span>{lines.length}L</span>
  </footer>

  <form class="cmdline" onsubmit={(e) => { e.preventDefault(); send() }}>
    {#if error}
      <span class="err">E: {error}</span>
    {:else}
      <span class="colon">:</span>
      <textarea
        rows="1"
        placeholder="reply"
        enterkeyhint="send"
        autocapitalize="off"
        spellcheck="false"
        bind:value={text}
        onkeydown={onKeydown}
        onfocus={() => (typing = true)}
        onblur={() => (typing = false)}
      ></textarea>
      <button class="send" type="submit" disabled={busy || !text.trim()}>&lt;CR&gt;</button>
    {/if}
  </form>
</div>

<style>
  .session {
    display: flex;
    flex-direction: column;
    height: 100dvh;
    padding-top: env(safe-area-inset-top);
  }
  .tabline {
    display: flex;
    align-items: center;
    gap: 1ch;
    background: var(--bg2);
    font-size: 13px;
    white-space: nowrap;
  }
  .tabline .back { color: var(--bg); background: var(--accent); text-decoration: none; font-weight: 700; padding: 6px 10px; }
  .tab { color: var(--bright); font-weight: 700; overflow: hidden; text-overflow: ellipsis; }
  .fill { flex: 1; }
  .tabline button { color: var(--muted); padding: 6px 4px; }
  .tabline button:last-child { padding-right: 10px; }
  .term {
    flex: 1;
    margin: 0;
    padding: 8px 10px;
    overflow: auto;
    font: 11.5px/1.35 var(--mono);
    color: var(--text);
    background: var(--bg);
    white-space: pre;
    -webkit-overflow-scrolling: touch;
  }
  .term.wrap { white-space: pre-wrap; word-break: break-word; }
  .keys {
    display: flex;
    gap: 2px;
    overflow-x: auto;
    padding: 4px 6px;
    scrollbar-width: none;
    border-top: 1px solid var(--line);
  }
  .keys button { flex: none; padding: 6px 5px; color: var(--muted); font-size: 14px; }
  .keys button:active { background: var(--cursor); color: var(--bright); }
  .keys.waiting button:nth-child(-n + 6) { color: var(--accent); font-weight: 700; }
  .cmdline {
    display: flex;
    align-items: flex-start;
    gap: 0.5ch;
    padding: 4px 8px calc(env(safe-area-inset-bottom) + 6px);
    background: var(--bg);
  }
  .colon { color: var(--bright); padding-top: 6px; }
  .err { color: var(--red); padding: 6px 0; font-size: 13px; }
  textarea {
    flex: 1;
    resize: none;
    max-height: 120px;
    font: 16px/1.4 var(--mono); /* 16px: iOS doesn't zoom in */
    color: var(--bright);
    background: transparent;
    border: 0;
    padding: 4px 0;
    caret-color: var(--accent);
    field-sizing: content;
  }
  textarea:focus { outline: none; }
  textarea::placeholder { color: var(--dim); }
  .send { color: var(--accent); padding: 6px 2px; font-size: 13px; }
  .send:disabled { color: var(--dim); }
  button:disabled { opacity: 0.5; }
</style>
