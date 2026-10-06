<script lang="ts">
  import { onDestroy } from 'svelte'
  import { api, poll, type Task } from './lib/api'
  import TaskList from './lib/TaskList.svelte'
  import Session from './lib/Session.svelte'

  let tasks = $state<Task[]>([])
  let error = $state('')
  let route = $state(location.hash)

  const stop = poll(async () => {
    try {
      tasks = await api.tasks()
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }, 2000)
  onDestroy(stop)

  const onHash = () => (route = location.hash)
  window.addEventListener('hashchange', onHash)
  onDestroy(() => window.removeEventListener('hashchange', onHash))

  let online = $state(navigator.onLine)
  const onOnline = () => (online = navigator.onLine)
  window.addEventListener('online', onOnline)
  window.addEventListener('offline', onOnline)
  onDestroy(() => {
    window.removeEventListener('online', onOnline)
    window.removeEventListener('offline', onOnline)
  })

  let openID = $derived(route.startsWith('#/t/') ? decodeURIComponent(route.slice(4)) : '')
  let open = $derived(tasks.find((t) => t.id === openID))
</script>

{#if !online}
  <div class="offline">offline: no connection to your Mac</div>
{/if}

{#if openID}
  <Session id={openID} task={open} />
{:else}
  <TaskList {tasks} {error} />
{/if}

<style>
  .offline {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 10;
    padding: 10px 16px calc(env(safe-area-inset-bottom) + 10px);
    background: var(--red);
    color: #fff;
    text-align: center;
  }
</style>
