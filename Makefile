PREFIX ?= $(HOME)/.local/bin

.PHONY: build install setup test e2e perf demo screenshots

build:
	go build -o bin/stackk ./cmd/stackk

# Build next to the target and rename: overwriting a running binary in place gets it killed on macOS.
install:
	go build -o $(PREFIX)/stackk.new ./cmd/stackk
	mv $(PREFIX)/stackk.new $(PREFIX)/stackk

setup: install
	$(PREFIX)/stackk setup

test:
	go test ./...

# End-to-end on an isolated tmux server with a fake agent (a few minutes each).
e2e:
	STACKK_QUIET_SECS=6 bash testdata/it.sh
	STACKK_QUIET_SECS=6 bash testdata/lifecycle.sh
	STACKK_QUIET_SECS=6 bash testdata/find.sh
	bash testdata/security.sh

# Performance budget (tmux calls per minute, hook latency).
perf:
	bash testdata/perf.sh

# Records the e2e run to recordings/stackk-e2e.mp4 (needs vhs).
demo:
	vhs testdata/demo.tape

# Regenerates the README screenshots in docs/ (needs vhs; runs on its own tmux server).
screenshots:
	vhs testdata/screenshot.tape
