# stackk: the CLI and dashboard live in cli/, the mobile web UI in web/.
PREFIX ?= $(HOME)/.local/bin

.PHONY: build install setup test e2e perf demo screenshots web web-install web-dev web-test web-screenshots

build install setup test e2e perf demo screenshots:
	$(MAKE) -C cli $@

# The web UI: the Svelte app is built into the Go server, which embeds it.
web:
	cd web/app && npm install --silent && npm run build
	cd web/server && go build -o bin/stackk-web .

web-install: web
	cp web/server/bin/stackk-web $(PREFIX)/stackk-web.new
	mv $(PREFIX)/stackk-web.new $(PREFIX)/stackk-web

# Vite with hot reload on :5173, its /api going to a stackk-web running on :7878.
web-dev:
	cd web/app && npm run dev

web-test: web
	bash web/test/api.sh

# README screenshot of the mobile UI (docs/mobile.png): a scene on its own tmux server, headless Chrome.
web-screenshots: web
	bash web/test/screenshot.sh
