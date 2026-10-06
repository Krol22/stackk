# stackk on your phone

Your stack in your pocket: see what waits for you, read what the agent said, approve a command or answer its question, reply, attach a screenshot. Start a few sessions in the morning, go ride, check in between.

![stackk mobile: the stack, a conversation, a permission and a question](../docs/mobile.png)

- **The stack first:** sessions waiting for you on top (permission, question, a finished turn), then the ones running, then scheduled and paused.
- **The conversation, not the terminal:** your prompts, the agent's replies in markdown, and its tools folded into one line per run.
- **Answer with your thumb:** a permission shows the full command with yes / always / no; a question shows its options as buttons. Both sit at the bottom, above the reply box.
- **Reply and attach:** type a reply, or add photos and files with `+`; the agent gets their paths on your Mac and reads them like any file.
- **An app on the home screen:** full screen, its own icon, opens instantly, says when your Mac is out of reach. Themes (auto, dark, light, gruvbox, nord, tokyo night) and text size in ⚙.

## How it works

`stackk-web` is a small Go server that runs on your Mac and serves the app (Svelte, built into the binary). It knows stackk only through the CLI: every request runs `stackk ls -json`, `transcript`, `send`, `key`, `upload`, `dirs`, `new`, `pause` and the like, and every 10 seconds `stackk reconcile`, which keeps the state current when no dashboard runs. The phone reaches it over [Tailscale](https://tailscale.com).

## Set up

```
make web-install                     # builds web/ into ~/.local/bin/stackk-web
stackk-web -allow you@gmail.com -devices iphone-15
tailscale serve --bg 7878            # https://<your-mac>.<tailnet>.ts.net
```

On the phone: open that address in Safari, then Share → **Add to Home Screen**. HTTPS from `tailscale serve` is what makes it a real app (it needs HTTPS certificates turned on for your tailnet: admin console → DNS → HTTPS Certificates).

## Security

Whoever reaches `stackk-web` can type into your agents, and your agents can run commands on your Mac. So:

- **It listens on localhost only.** Nothing outside your Mac reaches it until you publish it with `tailscale serve`, which keeps it inside your tailnet, over HTTPS.
- **`-devices iphone-15` lets in only the devices you name** (MagicDNS names or Tailscale IPs). **`-allow you@gmail.com` lets in only your Tailscale login**, as vouched for by `tailscale serve`: with it, a connection that didn't come through `tailscale serve` is refused. Use both. The Mac itself, on localhost, always works: anyone logged in to your Mac as another user could reach it too, so keep it on a Mac that is yours alone.
- **It answers only to its own names**: localhost and your tailnet's `*.ts.net` / Tailscale addresses. A web page you open on the Mac can't point its own domain at `127.0.0.1` (DNS rebinding) and drive the agents from your browser.
- **No other site can drive it.** Requests that change anything must be JSON (or a file upload) from the app's own origin: a link or a form on another page is refused before it reaches stackk.
- **Nothing from the request reaches a shell.** Task ids and key names are checked against a strict pattern and can't pass for CLI flags; replies and files go to the CLI on stdin. Replies are typed into the agent as text, with control characters removed, and can't pass for tmux options; a first prompt can't pass for an option of the agent's (`--dangerously-skip-permissions` is just text).
- **An answer goes to the prompt you saw.** Yes / always / no and question options carry which prompt they answer; if the agent moved on to another one meanwhile, the tap is refused instead of approving something you didn't read.
- **What agents write can't run in the app.** Their markdown is rendered through a sanitizer (DOMPurify): no scripts, no images, no embedded pages, links open outside the app.
- **Uploads stay private.** They go to `~/.local/state/stackk/uploads/<task>/` (only you can read it), at most 25 MB each.

## Develop

```
make web            # build the app and the server
make web-dev        # Vite with hot reload on :5173, its /api going to stackk-web on :7878
make web-test       # the server end to end, on an isolated tmux server with a fake agent
make web-screenshots  # regenerate docs/mobile.png
```
