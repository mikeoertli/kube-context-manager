# A fresh shell always starts local, even when its parent is in production.
export KCM_PROFILE=local KUBECONFIG=/dev/null
unset KCM_CONTEXT KCM_FILE KCM_EXPIRES_AT KCM_PREVIOUS_CONTEXT KCM_PREVIOUS_FILE
if [ -n "${ZSH_VERSION:-}" ]; then zmodload zsh/datetime; fi

# Starship's env_var modules need no subprocesses. Keep only the active slot
# exported, including when a child shell inherits its parent's prompt state.
_kcm_update_prompt() {
    local _kcm_slot=fallback _kcm_text _kcm_now _kcm_left _kcm_duration
    case "${KCM_PROMPT_ACTIVE_VAR:-}" in
        KCM_PROMPT_TEXT_*)
            case "$KCM_PROMPT_ACTIVE_VAR" in
                *[!a-zA-Z0-9_]*) ;;
                *) unset "$KCM_PROMPT_ACTIVE_VAR" ;;
            esac ;;
    esac
    unset KCM_PROMPT_ACTIVE_VAR
    [ "${KCM_PROMPT_ENABLED-1}" = 1 ] || return 0
    case "${KCM_PROFILE:-}" in
        '') return 0 ;;
        *[!a-zA-Z0-9_]*) ;;
        *) case " ${KCM_STARSHIP_PROFILES:-local dev qa prod other} " in
            *" $KCM_PROFILE "*) _kcm_slot=$KCM_PROFILE ;;
        esac ;;
    esac
    _kcm_text="${_KCM_PROFILE_EMOJI:+$_KCM_PROFILE_EMOJI }$KCM_PROFILE · ${KCM_CONTEXT:-—}"
    case "${KCM_EXPIRES_AT:-}" in
        ''|*[!0-9]*) ;;
        *)
            if [ -n "${ZSH_VERSION:-}" ]; then
                _kcm_now=$EPOCHSECONDS
            else
                printf -v _kcm_now '%(%s)T' -1
            fi
            _kcm_left=$((10#$KCM_EXPIRES_AT - _kcm_now))
            if [ "$_kcm_left" -le 0 ]; then
                _kcm_text="$_kcm_text · expired"
            else
                _kcm_duration="$((_kcm_left % 60))s"
                if [ "$_kcm_left" -ge 60 ]; then
                    _kcm_duration="$((_kcm_left / 60 % 60))m$_kcm_duration"
                fi
                if [ "$_kcm_left" -ge 3600 ]; then
                    _kcm_duration="$((_kcm_left / 3600))h$_kcm_duration"
                fi
                _kcm_text="$_kcm_text · $_kcm_duration left"
            fi ;;
    esac
    export KCM_PROMPT_ACTIVE_VAR="KCM_PROMPT_TEXT_$_kcm_slot"
    export "$KCM_PROMPT_ACTIVE_VAR=$_kcm_text"
}
_kcm_update_prompt

kcm() {
    local _kcm_code _kcm_arg _kcm_cmd=context _kcm_skip=0 _kcm_mutate=0
    for _kcm_arg in "$@"; do
        if [ "$_kcm_skip" = 1 ]; then _kcm_skip=0; continue; fi
        case "$_kcm_arg" in
            --settings) _kcm_skip=1 ;;
            --settings=*) ;;
            -*) ;;
            *) _kcm_cmd=$_kcm_arg; break ;;
        esac
    done
    case "$_kcm_cmd" in
        profile|context|ctx|clear|renew) _kcm_mutate=1 ;;
    esac
    for _kcm_arg in "$@"; do
        case "$_kcm_arg" in -h|--help) _kcm_mutate=0 ;; esac
    done
    if [ "$_kcm_mutate" = 1 ]; then
        _kcm_code=$("$_KCM_BIN" __shell "$@") || return $?
        eval "$_kcm_code"
        _kcm_update_prompt
    else
        "$_KCM_BIN" "$@"
    fi
}

# Context flags only: these wrappers do not gate commands or check timeouts.
# Explicit flags supplied by the caller take precedence.
kcmkubectl() {
    if [ -n "${KCM_CONTEXT:-}" ]; then
        command kubectl --context "$KCM_CONTEXT" "$@"
    else
        command kubectl "$@"
    fi
}
kcmhelm() {
    if [ -n "${KCM_CONTEXT:-}" ]; then
        command helm --kube-context "$KCM_CONTEXT" "$@"
    else
        command helm "$@"
    fi
}
kcmk9s() {
    if [ -n "${KCM_CONTEXT:-}" ]; then
        command k9s --context "$KCM_CONTEXT" "$@"
    else
        command k9s "$@"
    fi
}

_kcm_expire() {
    local _kcm_now
    case "${KCM_EXPIRES_AT:-}" in ''|*[!0-9]*) return 0 ;; esac
    if [ -n "${ZSH_VERSION:-}" ]; then
        _kcm_now=${EPOCHSECONDS:-0}
    else
        printf -v _kcm_now '%(%s)T' -1
    fi
    if [ "$_kcm_now" -lt "$KCM_EXPIRES_AT" ]; then return 0; fi
    export KCM_PROFILE=local KUBECONFIG=/dev/null
    unset KCM_CONTEXT KCM_FILE KCM_EXPIRES_AT KCM_PREVIOUS_CONTEXT KCM_PREVIOUS_FILE
    _KCM_PROFILE_EMOJI=${_KCM_LOCAL_EMOJI:-🏠}
    _kcm_update_prompt
    printf '\n🏠 kcm: session expired; switched to local. Select a context with kcm.\n' >&2
    return 1
}
