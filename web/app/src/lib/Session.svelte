<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte'
  import { api, poll, type Message, type Task } from './api'
  import { md } from './markdown'

  let { id, task }: { id: string; task?: Task } = $props()

  let messages = $state<Message[]>([])
  let asksTrust = $state(false) // a new session waiting on Claude's "do you trust this folder?"
  let error = $state('')
  let text = $state('')
  let busy = $state(false)
  let menu = $state(false)
  let confirmClose = $state(false)
  let renaming = $state<string | null>(null) // the new name while the rename field is open

  function rename() {
    const name = renaming?.trim()
    if (!name) return
    act(async () => {
      await api.rename(id, name)
      renaming = null
      menu = false
    })
  }
  let files = $state<{ name: string; path: string }[]>([])
  let uploading = $state(0)
  let picker: HTMLInputElement | undefined = $state()
  let expanded = $state<Record<number, boolean>>({})
  let scroller: HTMLElement | undefined = $state()
  let root: HTMLElement | undefined = $state()

  // With the keyboard open, iOS shrinks only the visible part of the page and scrolls it to the
  // input: fit the view to what is left above the keyboard, header included. Otherwise the view is
  // pinned to the window's edges (CSS).
  let keyboard = $state(false)
  function fit() {
    const vv = window.visualViewport
    if (!vv || !root) return
    keyboard = vv.height < window.innerHeight - 120
    if (keyboard && vv.offsetTop > 0) window.scrollTo(0, 0)
    root.style.height = keyboard ? `${vv.height}px` : ''
    root.style.transform = keyboard && vv.offsetTop > 0 ? `translateY(${vv.offsetTop}px)` : ''
  }
  // The keyboard animates in: fit again once it has settled.
  const refit = () => [0, 100, 350].forEach((ms) => setTimeout(fit, ms))

  onMount(() => {
    const vv = window.visualViewport
    if (!vv) return
    fit()
    vv.addEventListener('resize', fit)
    vv.addEventListener('scroll', fit)
    return () => {
      vv.removeEventListener('resize', fit)
      vv.removeEventListener('scroll', fit)
    }
  })
  let stuck = true // follow the bottom until you scroll up

  async function refresh() {
    try {
      const tr = await api.transcript(id)
      messages = tr.messages
      asksTrust = !!tr.asks_trust
      error = ''
      if (stuck) {
        await tick()
        scroller?.scrollTo({ top: scroller.scrollHeight })
      }
    } catch (e) {
      error = (e as Error).message
    }
  }

  onDestroy(poll(refresh, 1500))

  function onScroll() {
    if (!scroller) return
    stuck = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 40
  }

  async function act(fn: () => Promise<unknown>) {
    busy = true
    try {
      await fn()
      error = ''
      stuck = true
      setTimeout(refresh, 300)
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  // An answer to the prompt on screen, and to no other: refused if the agent moved on meanwhile.
  const press = (k: string) => act(() => api.keys(id, [k], { waiting: task?.waiting_id }))
  // Claude picks option n with the digit n. omp's ask takes no digits and starts on the recommended
  // option: go to the top (the list doesn't wrap), then down to the option.
  const optionKeys = (i: number, n: number) =>
    task?.agent === 'omp' ? [...Array(n + 1).fill('up'), ...Array(i).fill('down'), 'enter'] : [String(i + 1)]
  const choose = (i: number, n: number) => act(() => api.keys(id, optionKeys(i, n), { waiting: task?.waiting_id }))

  // hide and rm take the session off the list: back to it once done.
  async function leave(action: 'hide' | 'rm') {
    busy = true
    try {
      await api.action(id, action)
      location.hash = '#/'
    } catch (e) {
      error = (e as Error).message
      menu = confirmClose = false
    } finally {
      busy = false
    }
  }

  // Files go to the Mac first (stackk upload); the reply carries their paths, which the agent
  // reads like any file (screenshots included).
  async function attach(list: FileList | null) {
    for (const f of Array.from(list ?? [])) {
      uploading++
      try {
        const { path } = await api.upload(id, f)
        files = [...files, { name: f.name, path }]
      } catch (e) {
        error = `${f.name}: ${(e as Error).message}`
      } finally {
        uploading--
      }
    }
    if (picker) picker.value = ''
  }

  let input: HTMLTextAreaElement | undefined = $state()

  function send() {
    if (busy) return
    // The field itself, not the bound copy: iOS commits a suggested word only after the key.
    if (input) text = input.value
    const t = text.trim()
    if (!t && !files.length) return
    const paths = files.map((f) => f.path).join(' ')
    const msg = files.length ? `${t || 'See the attached files.'} (attached: ${paths})` : t
    act(async () => {
      if (question && !stepping) {
        // Your own answer: the option after the listed ones (Claude's "Type something", omp's
        // "Other") takes the text.
        const n = question.options?.length ?? 0
        await api.keys(id, optionKeys(n, n), { waiting: task?.waiting_id })
        await new Promise((r) => setTimeout(r, 300))
      }
      await api.send(id, msg)
      text = ''
      files = []
    })
  }

  function onKeydown(e: KeyboardEvent) {
    // While a word is being composed (suggestions, autocorrect) Enter belongs to the keyboard.
    if (e.isComposing || e.keyCode === 229) return
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  // Runs of tool calls fold into one line: the conversation stays readable on a phone.
  type Item = { kind: 'msg'; m: Message } | { kind: 'tools'; at: number; tools: Message[] }
  let items = $derived.by(() => {
    const out: Item[] = []
    messages.forEach((m, i) => {
      const last = out[out.length - 1]
      if (m.role === 'tool' && last?.kind === 'tools') last.tools.push(m)
      else if (m.role === 'tool') out.push({ kind: 'tools', at: i, tools: [m] })
      else out.push({ kind: 'msg', m })
    })
    return out
  })
  const toolMark = (m: Message) => (m.error ? '✗' : m.done ? '✓' : '…')


  let waiting = $derived(task?.phase === 'waiting')
  let ask = $derived(waiting ? task?.waiting : undefined)
  let question = $derived(ask?.questions?.[0])
  // Several questions, or several answers to one: the prompt steps through them, so offer the
  // keys it takes rather than buttons that would only fit its first screen.
  let stepping = $derived((ask?.questions?.length ?? 0) > 1 || !!question?.multiSelect)
  const stepKeys: [string, string][] = [['up', '↑'], ['down', '↓'], ['space', 'select'], ['tab', 'next'], ['enter', 'submit'], ['esc', 'cancel']]
  let paused = $derived(task?.view === 'paused' || task?.run_state === 'exited')
</script>

<div class="session" bind:this={root}>
  <header>
    <a class="back" href="#/" aria-label="back">‹</a>
    <span class="name">{task?.name ?? id}</span>
    <button class="more" aria-label="more" onclick={() => { menu = !menu; confirmClose = false; renaming = null }}>⋯</button>
  </header>

  {#if menu}
    <div class="menu">
      {#if paused}
        <button disabled={busy} onclick={() => { menu = false; act(() => api.action(id, 'resume')) }}>resume</button>
      {:else}
        <button disabled={busy} onclick={() => { menu = false; act(() => api.action(id, 'pause')) }}>pause</button>
      {/if}
      {#if task?.priority}
        <button disabled={busy} onclick={() => { menu = false; act(() => api.action(id, 'unpriority')) }}>priority: off</button>
      {:else}
        <button disabled={busy} onclick={() => { menu = false; act(() => api.action(id, 'priority')) }}>priority <span class="hint">first on the stack</span></button>
      {/if}
      {#if renaming === null}
        <button disabled={busy} onclick={() => (renaming = task?.name ?? '')}>rename</button>
      {:else}
        <form class="rename" onsubmit={(e) => { e.preventDefault(); rename() }}>
          <!-- svelte-ignore a11y_autofocus -->
          <input bind:value={renaming} autofocus autocapitalize="off" spellcheck="false" enterkeyhint="done" />
          <button type="submit" disabled={busy || !renaming.trim()}>save</button>
        </form>
      {/if}
      {#if task?.view === 'hidden'}
        <button disabled={busy} onclick={() => { menu = false; act(() => api.action(id, 'unhide')) }}>unhide</button>
      {:else}
        <button disabled={busy} onclick={() => leave('hide')}>hide <span class="hint">not a task: out of every list</span></button>
      {/if}
      <button class="danger" disabled={busy} onclick={() => (confirmClose ? leave('rm') : (confirmClose = true))}>
        {confirmClose ? 'tap again to close it for good' : 'close & remove'}
        {#if !confirmClose}<span class="hint">ends the agent, closes its window</span>{/if}
      </button>
    </div>
  {/if}

  <div class="scroll chat" bind:this={scroller} onscroll={onScroll}>
      {#each items as it, n (n)}
        {#if it.kind === 'tools'}
          {#if expanded[it.at] || it.tools.length === 1}
            {#each it.tools as m, j (j)}
              <button class="tool" class:err={m.error} onclick={() => (expanded[it.at] = false)}>{toolMark(m)} {m.tool}</button>
            {/each}
          {:else}
            <button class="tool" onclick={() => (expanded[it.at] = true)}>
              {toolMark(it.tools[it.tools.length - 1])} {it.tools.length} tools · {it.tools[it.tools.length - 1].tool}
            </button>
          {/if}
        {:else if it.m.role === 'user'}
          <div class="you">{it.m.text}</div>
        {:else}
          <div class="agent">{@html md(it.m.text ?? '')}</div>
        {/if}
      {:else}
        <p class="dim">no messages yet</p>
      {/each}
      {#if task?.phase === 'working'}<p class="working">working…</p>{/if}
    </div>

  <div class="dock" class:keyboard>
    {#if error}<button class="error" onclick={() => (error = '')}>{error}</button>{/if}

    {#if asksTrust}
      <div class="ask">
        <p class="q">Claude asks: do you trust this folder?</p>
        <pre class="cmd">{task?.cwd ?? ''}</pre>
        <div class="row">
          <button class="choice yes" disabled={busy} onclick={() => act(() => api.keys(id, ['down', 'enter'], { trust: true }))}>trust</button>
          <button class="choice no" disabled={busy} onclick={() => act(() => api.keys(id, ['esc'], { trust: true }))}>no, exit</button>
        </div>
      </div>
    {:else if waiting}
      <!-- what the agent waits for, where your thumb is -->
      <div class="ask">
        {#if stepping}
          {#each ask?.questions ?? [] as q, n (n)}
            <p class="q">{n + 1}. {q.question}</p>
            <p class="opts">{(q.options ?? []).map((o, i) => `${i + 1} ${o.label}`).join(' · ')}</p>
          {/each}
          <div class="keyrow">
            {#each stepKeys as [k, label] (k)}
              <button class="choice" disabled={busy} onclick={() => press(k)}>{label}</button>
            {/each}
          </div>
        {:else if question}
          <p class="q">{question.question}</p>
          {#each question.options ?? [] as o, i (i)}
            <button class="choice" disabled={busy} onclick={() => choose(i, question.options?.length ?? 0)}>
              <b>{o.label}</b>{#if o.description}<span class="d">{o.description}</span>{/if}
            </button>
          {/each}
          <button class="choice ghost" disabled={busy} onclick={() => input?.focus()}>type your own answer…</button>
          <button class="choice ghost" disabled={busy} onclick={() => press('esc')}>cancel</button>
        {:else if ask?.tool}
          <p class="q">{ask.tool}</p>
          {#if ask.input}<pre class="cmd">{ask.input}</pre>{/if}
          <div class="row">
            <button class="choice yes" disabled={busy} onclick={() => press('1')}>yes</button>
            <button class="choice" disabled={busy} onclick={() => press('2')}>always</button>
            <button class="choice no" disabled={busy} onclick={() => press('esc')}>no</button>
          </div>
        {:else}
          <p class="q">{task?.last_msg || 'waiting for you'}</p>
        {/if}
      </div>
    {/if}

    {#if files.length || uploading}
      <div class="files">
        {#each files as f, i (f.path)}
          <span class="file">{f.name}<button aria-label="remove" onclick={() => (files = files.filter((_, j) => j !== i))}>×</button></span>
        {/each}
        {#if uploading}<span class="file dim">uploading…</span>{/if}
      </div>
    {/if}

    <form class="reply" onsubmit={(e) => { e.preventDefault(); send() }}>
      <input bind:this={picker} type="file" multiple hidden onchange={(e) => attach(e.currentTarget.files)} />
      <button type="button" class="clip" aria-label="attach" disabled={busy} onclick={() => picker?.click()}>+</button>
      <textarea
        rows="1"
        placeholder={question && !stepping ? 'your own answer…' : 'reply…'}
        enterkeyhint="send"
        autocapitalize="off"
        spellcheck="false"
        bind:value={text}
        bind:this={input}
        disabled={asksTrust}
        onkeydown={onKeydown}
        onfocus={refit}
        onblur={() => { window.scrollTo(0, 0); refit() }}
      ></textarea>
      <button class="send" type="submit" disabled={busy || uploading > 0 || (!text.trim() && !files.length)} aria-label="send">↑</button>
    </form>
  </div>
</div>

<style>
  /* pinned to the screen's edges: in a home-screen app iOS reports 100dvh short of the screen */
  .session { position: fixed; inset: 0; display: flex; flex-direction: column; }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: env(safe-area-inset-top) 4px 0 0;
    border-bottom: 1px solid var(--line);
  }
  .back {
    display: grid;
    place-items: center;
    width: var(--tap);
    height: var(--tap);
    color: var(--accent);
    text-decoration: none;
    font-size: 26px;
  }
  .name { flex: 1; min-width: 0; color: var(--bright); font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .more { width: var(--tap); height: var(--tap); color: var(--muted); font-size: 20px; }
  .menu { display: flex; flex-direction: column; border-bottom: 1px solid var(--line); background: var(--raised); }
  .menu button { min-height: var(--tap); padding: 0 16px; text-align: left; }
  .menu .hint { color: var(--dim); margin-left: 1ch; }
  .menu .danger { color: var(--red); }
  .rename { display: flex; gap: 8px; padding: 6px 12px 6px 16px; }
  .rename input { flex: 1; min-width: 0; font-size: 16px; color: var(--bright); background: var(--bg); border: 1px solid var(--line); border-radius: 10px; padding: 8px 10px; }
  .rename input:focus { outline: none; border-color: var(--dim); }
  .rename button { color: var(--accent); padding: 0 8px; }

  .scroll { flex: 1; overflow: auto; -webkit-overflow-scrolling: touch; margin: 0; }
  .chat { padding: 8px 16px 16px; }
  .you { color: var(--accent); white-space: pre-wrap; word-break: break-word; margin: 18px 0 6px; padding-left: 10px; border-left: 2px solid var(--accent); }
  .agent { word-break: break-word; margin: 8px 0; }
  .agent :global(p) { margin: 0 0 8px; }
  .agent :global(ul), .agent :global(ol) { margin: 0 0 8px; padding-left: 2.2ch; }
  .agent :global(li) { margin: 2px 0; }
  .agent :global(strong) { color: var(--bright); }
  .agent :global(em) { color: var(--bright); }
  .agent :global(code) { color: var(--aqua); background: var(--raised); border-radius: 4px; padding: 0 3px; }
  .agent :global(pre) { background: var(--raised); border-radius: 8px; padding: 8px 10px; overflow-x: auto; margin: 0 0 8px; }
  .agent :global(pre code) { background: none; padding: 0; }
  .agent :global(h1), .agent :global(h2), .agent :global(h3), .agent :global(h4) { font-size: inherit; color: var(--bright); margin: 12px 0 6px; }
  .agent :global(a) { color: var(--blue); }
  .agent :global(blockquote) { margin: 0 0 8px; padding-left: 10px; border-left: 2px solid var(--line); color: var(--muted); }
  .agent :global(table) { border-collapse: collapse; display: block; overflow-x: auto; margin: 0 0 8px; }
  .agent :global(th), .agent :global(td) { border: 1px solid var(--line); padding: 2px 6px; text-align: left; }
  .agent :global(hr) { border: 0; border-top: 1px solid var(--line); }
  .tool {
    display: block;
    width: 100%;
    text-align: left;
    color: var(--dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    padding: 4px 0;
  }
  .tool.err { color: var(--red); }
  .dim { color: var(--dim); }
  .working { color: var(--blue); }

  .dock { border-top: 1px solid var(--line); padding: 8px 12px max(env(safe-area-inset-bottom), 8px); background: var(--bg); }
  .dock.keyboard { padding-bottom: 8px; } /* the home bar's margin is under the keyboard */
  .error { display: block; width: 100%; text-align: left; color: var(--red); padding: 4px 0 8px; }
  .ask { padding-bottom: 8px; }
  .q { margin: 2px 0 8px; color: var(--bright); }
  .cmd { margin: 0 0 8px; padding: 8px 10px; background: var(--raised); color: var(--aqua); white-space: pre-wrap; word-break: break-word; max-height: 25dvh; overflow: auto; border-radius: 8px; font: inherit; }
  .row { display: flex; gap: 8px; }
  .opts { margin: -4px 0 8px; color: var(--muted); }
  .keyrow { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; }
  .keyrow .choice { text-align: center; margin: 0; }
  .row .choice { flex: 1; text-align: center; }
  .choice {
    display: block;
    width: 100%;
    min-height: 48px;
    margin-bottom: 8px;
    padding: 10px 14px;
    text-align: left;
    border-radius: 10px;
    background: var(--raised);
    border: 1px solid var(--line);
  }
  .choice:active { background: var(--press); }
  .choice b { color: var(--bright); }
  .choice .d { display: block; color: var(--muted); font-size: inherit; }
  .choice.yes { background: var(--accent); color: var(--bg); border-color: var(--accent); font-weight: 700; }
  .choice.no { color: var(--red); }
  .choice.ghost { background: none; color: var(--muted); }
  .reply { display: flex; align-items: flex-end; gap: 8px; }
  .clip { width: var(--tap); height: var(--tap); border-radius: 12px; background: var(--raised); border: 1px solid var(--line); color: var(--muted); font-size: 22px; }
  .files { display: flex; flex-wrap: wrap; gap: 6px; padding-bottom: 8px; }
  .file { display: inline-flex; align-items: center; gap: 4px; max-width: 100%; padding: 4px 4px 4px 10px; border-radius: 8px; background: var(--raised); border: 1px solid var(--line); color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .file button { width: 28px; height: 28px; color: var(--muted); }
  .file.dim { color: var(--dim); padding-right: 10px; }
  textarea {
    flex: 1;
    min-height: var(--tap);
    max-height: 30dvh;
    resize: none;
    font-size: 16px; /* iOS doesn't zoom in at 16px */
    line-height: 1.4;
    color: var(--bright);
    background: var(--raised);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 10px 12px;
    field-sizing: content;
  }
  textarea:focus { outline: none; border-color: var(--dim); }
  textarea::placeholder { color: var(--dim); }
  .send {
    width: var(--tap);
    height: var(--tap);
    border-radius: 12px;
    background: var(--accent);
    color: var(--bg);
    font-size: 20px;
    font-weight: 700;
  }
  .send:disabled { background: var(--raised); color: var(--dim); }
  button:disabled { opacity: 0.6; }
</style>
