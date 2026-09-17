if [[ -o interactive ]]; then
    autoload -Uz add-zsh-hook
    _kcm_precmd() {
        _kcm_expire
        _kcm_update_prompt
        return 0
    }
    add-zsh-hook -d precmd _kcm_precmd 2>/dev/null
    add-zsh-hook precmd _kcm_precmd
    if [[ ${_KCM_ZLE_INSTALLED:-0} != 1 ]]; then
        zle -A accept-line _kcm_original_accept_line
        _KCM_ZLE_INSTALLED=1
    fi
    _kcm_accept_line() {
        if ! _kcm_expire; then
            BUFFER=''
            print -u2 'kcm: submitted command cancelled. Enter your next command in local.'
            zle send-break
            return
        fi
        zle _kcm_original_accept_line
    }
    zle -N accept-line _kcm_accept_line
fi
"$_KCM_BIN" doctor --quiet
