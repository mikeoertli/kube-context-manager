# Run from the repository root: make demo-gif
# All KCM actions run in demo/run.sh's disposable environment.
Output assets/demo.gif
Require zsh
Require fzf
Require python3
Set Shell "bash"
Set Width 1440
Set Height 900
Set FontSize 22
Set FontFamily "monospace"
Set Theme "Catppuccin Mocha"
Set Padding 24
Set WindowBar Colorful
Set Framerate 10
Set TypingSpeed 35ms
Env BASH_ENV "/dev/null"
Env ENV "/dev/null"

Hide
Type "bash demo/run.sh"
Enter
Wait+Screen /normal default: 8 hours/
Sleep 300ms
Show
Sleep 2s
Type "kcm list"
Enter
Sleep 4s

Hide
Ctrl+L
Show
Type "# Choose a profile AND context in one flow"
Enter
Type "kcm profile"
Enter
Wait+Screen /Profile for this shell/
Sleep 700ms
Type "dev"
Sleep 1500ms
Enter
Wait+Screen /Context · dev/
Sleep 700ms
Type "api-dev"
Sleep 1500ms
Enter
Sleep 1500ms

Type "# Shared namespaces, with an interactive picker"
Enter
Type "kcm ns"
Enter
Wait+Screen /Namespace · dev/
Sleep 700ms
Down
Sleep 1500ms
Enter
Type "kcm status"
Enter
Sleep 3s

Hide
Ctrl+L
Show
Type "# Import and rename a kubeconfig into its profile"
Enter
Type "kcm install incoming.yaml --profile qa --rename-context api-qa --no-prompt"
Enter
Sleep 1200ms
Type "kcm profile qa"
Enter
Type "kcm contexts"
Enter
Sleep 3s

Hide
Ctrl+L
Show
Type "# Production resets to local after expiry (6s in this demo)"
Enter
Type "kcm profile"
Enter
Wait+Screen /Profile for this shell/
Type "prod"
Sleep 1500ms
Enter
Wait+Screen /Context · prod/
Type "api-prod"
Sleep 1500ms
Enter
Sleep 500ms
Type "kcm status"
Enter
Sleep 7s
Type "kcmkubectl get pods"
Enter
Wait+Screen /submitted command cancelled/
Sleep 3s
Type "kcm status"
Enter
Sleep 3s
Type "# Back to local. Other shells are unchanged."
Enter
Sleep 2s
Hide
Type "exit"
Enter
