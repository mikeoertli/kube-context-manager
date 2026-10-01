# Loaded only by demo/run.sh inside the disposable shell.
cd -- "$KCM_DEMO_DIR"
setopt interactivecomments
unsetopt beep
bindkey -e
HISTFILE=/dev/null
export KUBECONFIG=/dev/null
# Check generated code succeeded before evaluating it.
_kcm_demo_init=$(command kcm init zsh) || exit 1
eval "$_kcm_demo_init"
unset _kcm_demo_init
_kcm_demo_prompt() {
    local text=${(P)KCM_PROMPT_ACTIVE_VAR}
    PROMPT="%F{cyan}${text//\%/%%}%f"$'\n''%F{green}❯%f '
}
add-zsh-hook precmd _kcm_demo_prompt
printf '\033[2J\033[H'
print -P '%B KCM · shell-local Kubernetes contexts%b'
print ' Synthetic kubeconfigs • simulated namespace API • no cluster required'
print ' Demo production timeout: 6 seconds (normal default: 8 hours).'
print ' Type exit to leave; all demo files are removed automatically.'
print
