# vhs testdata/demo.tape — records the canvas demo to recordings/stackk-canvas.mp4
Output recordings/stackk-canvas.mp4
Set Shell "bash"
Set FontSize 14
Set Width 1600
Set Height 980
Set Padding 12
Set WaitTimeout 600s
Hide
Type "clear"
Enter
Sleep 1s
Show
Type "bash testdata/demo.sh testdata/canvas-demo.sh"
Sleep 500ms
Enter
Wait+Screen /DEMO-KONIEC/
Sleep 6s
