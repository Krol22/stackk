package ui

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// Slot is the placeholder process that holds a window's place while its agent is shown in the
// dashboard's drawer. name() tells which task that is; Enter or a click calls recall() to bring
// the agent back into this window. recall may be nil (the stack's info window).
func Slot(msg func() string, recall func()) {
	fd := int(os.Stdin.Fd())
	if recall != nil {
		if old, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, old)
		}
		fmt.Print("\x1b[?1000h\x1b[?1006h") // mouse clicks
		defer fmt.Print("\x1b[?1000l\x1b[?1006l")
	}
	draw := func() {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			w, h = 80, 24
		}
		fmt.Print("\x1b[2J\x1b[H\x1b[?25l")
		lines := strings.Split(msg(), "\n")
		fmt.Print(strings.Repeat("\r\n", max(0, h/2-len(lines))))
		for i, l := range lines {
			color := "106;106;106"
			if i == 0 {
				color = "224;180;76"
			}
			fmt.Printf("\x1b[38;2;%sm%s%s\x1b[0m\r\n", color, strings.Repeat(" ", max(0, (w-len([]rune(l)))/2)), l)
		}
	}
	draw()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	keys := make(chan byte, 16)
	if recall != nil {
		go func() {
			buf := make([]byte, 64)
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil {
					return
				}
				for _, b := range buf[:n] {
					keys <- b
				}
			}
		}()
	}
	var seq []byte
	for {
		select {
		case s := <-sig:
			if s != syscall.SIGWINCH {
				return
			}
			draw()
		case b := <-keys:
			seq = append(seq, b)
			str := string(seq)
			switch {
			case b == '\r' || b == '\n':
				seq = nil
				recall()
				draw()
			case strings.HasPrefix(str, "\x1b[<0;") && strings.HasSuffix(str, "M"): // left press
				seq = nil
				recall()
				draw()
			case b == 'm' || b == 'M' || len(seq) > 32 || (len(seq) == 1 && b != 0x1b):
				seq = nil
			}
		}
	}
}
