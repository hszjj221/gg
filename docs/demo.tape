# Re-record the README demo from the repository root:
#
#   export OPENAI_API_KEY=...     # any OpenAI-compatible key gg already accepts
#   go install github.com/hszjj221/gg/cmd/gg@latest
#   vhs docs/demo.tape
#
# vhs (https://github.com/charmbracelet/vhs) drives a real terminal. This
# script was not recorded in CI: a typical session needs a live model, and
# the sleeps below are estimates. If "Approve? [y/N]" appears earlier or
# later than the second Sleep, change that Sleep before typing y.
#
# The first prompt asks for a read of cmd/gg/main.go (no edit, no bash).
# The second asks for `go test`, which is not on the read-only allowlist,
# so --approval auto should print an approval prompt. Answer y.

Output docs/demo.gif

Require gg

Set Shell bash
Set FontSize 18
Set Width 1100
Set Height 680
Set Padding 28
Set TypingSpeed 40ms

Type "gg"
Enter
Sleep 1s

Type "Read cmd/gg/main.go and explain how Ctrl+C is handled. Do not edit files or run bash."
Enter
Sleep 20s

Type "Run go test ./internal/tools -count=1 -timeout 60s and report whether it passed. Do not change files."
Enter
Sleep 12s
Type "y"
Enter
Sleep 25s

Ctrl+D
Sleep 1s
