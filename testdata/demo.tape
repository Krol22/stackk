# vhs testdata/demo.tape — records the e2e suite to recordings/stackk-e2e.mp4
Output recordings/stackk-e2e.mp4
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
Type "bash testdata/demo.sh"
Sleep 500ms
Enter
Wait+Screen /DEMO-KONIEC/
Sleep 6s
