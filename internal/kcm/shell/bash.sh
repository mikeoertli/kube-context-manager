if [[ $- == *i* ]]; then
    if (( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 4) )); then
        printf 'kcm: interactive timeout support requires bash 4.4+ (macOS: brew install bash).\n' >&2
        return 1
    fi
    _kcm_precmd() {
        local _kcm_status=$?
        _kcm_ensure_keymaps
        _kcm_expire
        return "$_kcm_status"
    }
    _kcm_accept_check() {
        if ! _kcm_expire; then
            READLINE_LINE=''
            READLINE_POINT=0
            printf 'kcm: submitted command cancelled. Enter your next command in local.\n' >&2
        fi
    }
    # A readline macro runs the check before accept-line. Clearing READLINE_LINE
    # cancels the whole command, including pipelines and semicolon lists.
    _kcm_ensure_keymaps() {
        local _kcm_map _kcm_mode=emacs
        [[ -o vi ]] && _kcm_mode=vi
        [[ ${_KCM_KEYMAP_MODE:-} == "$_kcm_mode" ]] && return 0
        for _kcm_map in emacs-standard vi-insert vi-command; do
            bind -m "$_kcm_map" -x '"\C-x\C-k":_kcm_accept_check'
            bind -m "$_kcm_map" '"\C-x\C-j":accept-line'
            bind -m "$_kcm_map" '"\C-m":"\C-x\C-k\C-x\C-j"'
            bind -m "$_kcm_map" '"\C-j":"\C-x\C-k\C-x\C-j"'
        done
        _KCM_KEYMAP_MODE=$_kcm_mode
    }
    unset _KCM_KEYMAP_MODE
    _kcm_ensure_keymaps
    if [[ ${_KCM_BASH_INSTALLED:-0} != 1 ]]; then
        if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == 'declare -a '* ]]; then
            PROMPT_COMMAND=(_kcm_precmd "${PROMPT_COMMAND[@]}")
        else
            PROMPT_COMMAND="_kcm_precmd${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
        fi
        _KCM_BASH_INSTALLED=1
    fi
fi
"$_KCM_BIN" doctor --quiet
