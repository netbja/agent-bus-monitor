# Renders docs/assets/busmon.png from the fictional demo bus (needs vhs, ttyd, ffmpeg).
#   scripts/busmon-demo.sh up && vhs scripts/busmon-demo.tape && scripts/busmon-demo.sh down
Output "/tmp/busmon-demo.gif"
Set Shell "bash"
Set FontSize 16
Set Width 1800
Set Height 700
Set Padding 24
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set BorderRadius 10
Hide
Type "export AGENT_BUS_NO_UPDATE_CHECK=1 TERM=xterm-256color; clear" Enter
Type "scripts/busmon-demo.sh run --limit 25" Enter
Show
Sleep 4s
Screenshot "docs/assets/busmon.png"
Sleep 500ms
