# Recorded with VHS: https://github.com/charmbracelet/vhs
#
#   vhs demo.tape
#
# `fth' with no arguments publishes the directory it runs in, so this records
# /tmp/demo as a repository called "demo". Two questions get answered with the
# arrow keys: which instance to create on, and which clone URL to wire up.
#
# It creates a real repository on gitea.com and leaves it up afterwards,
# because that is the point of the demo. Delete it again with:
#
#   tea repos delete --owner <you> --name demo --force

Output demo.gif

Set Shell "bash"
Set FontSize 15
Set FontFamily "JetBrainsMono Nerd Font"
Set Theme "catppuccin-mocha"
Set Width 1120
Set Height 490
Set PlaybackSpeed 1

# Start from an empty directory, then wipe the screen so the setup is not part
# of the recording. It types at no speed worth watching.
Set TypingSpeed 1ms
Type "rm -rf /tmp/demo && mkdir -p /tmp/demo && cd /tmp/demo"
Enter
Sleep 1500ms
Ctrl+L

Set TypingSpeed 135ms
Type "fth"
Sleep 250ms
Enter

# Probing all five CLIs takes about five seconds. The instance picker then
# draws with fj/codeberg.org first and tea/gitea.com second; the key press is
# buffered by then, so it lands as soon as the picker is up.
Sleep 5s
Down
Sleep 700ms
Enter

# The clone URL picker. Step onto https and back to show it moves, then take
# the ssh default.
Sleep 800ms
Down
Sleep 600ms
Up
Sleep 600ms
Enter

# Create, scaffold, push: about five seconds of real work.
Sleep 6s
