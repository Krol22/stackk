# vhs testdata/screenshot.tape — README screenshots into docs/ (the scene: testdata/screenshot.sh)
Output recordings/screenshot-run.gif
Set Shell "bash"
Set FontSize 15
Set FontFamily "JetBrains Mono"
Set Width 1720
Set Height 1000
Set Padding 18
Set Margin 48
Set MarginFill "#5b4a8a"
Set BorderRadius 12
Set WindowBar Colorful
Set WindowBarSize 40
Set Theme { "name": "stackk", "background": "#161616", "foreground": "#d4d4d4", "cursor": "#d4d4d4", "selection": "#3a3a3a", "black": "#161616", "red": "#e27878", "green": "#8fcf7a", "yellow": "#e0b44c", "blue": "#7aa2f7", "magenta": "#a99ae6", "cyan": "#7dcfff", "white": "#d4d4d4", "brightBlack": "#6a6a6a", "brightRed": "#e27878", "brightGreen": "#8fcf7a", "brightYellow": "#e0b44c", "brightBlue": "#7aa2f7", "brightMagenta": "#a99ae6", "brightCyan": "#7dcfff", "brightWhite": "#f2f2f2" }
Set WaitTimeout 180s
Hide
Type "clear && bash testdata/shot-run.sh"
Enter
Sleep 2s
Show
Wait+Screen /migrate-postgres-16/
Sleep 14s
Screenshot docs/dashboard.png
Sleep 1s
Wait+Screen /ranked by AI/
Sleep 2s
Screenshot docs/find.png
Sleep 1s
Wait+Screen /CONCURRENTLY/
Sleep 3s
Screenshot docs/drawer.png
Sleep 2s
