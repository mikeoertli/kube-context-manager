# A fresh shell always starts local, even when its parent is in production.
export KCM_PROFILE=local KUBECONFIG=/dev/null
unset KCM_CONTEXT KCM_FILE KCM_EXPIRES_AT KCM_PREVIOUS_CONTEXT KCM_PREVIOUS_FILE

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
    else
        "$_KCM_BIN" "$@"
    fi
}

# Context flags only: these wrappers do not gate commands or check timeouts.
# Explicit flags supplied by the caller take precedence.
kubectl() {
    if [ -n "${KCM_CONTEXT:-}" ]; then
        command kubectl --context "$KCM_CONTEXT" "$@"
    else
        command kubectl "$@"
    fi
}
helm() {
    if [ -n "${KCM_CONTEXT:-}" ]; then
        command helm --kube-context "$KCM_CONTEXT" "$@"
    else
        command helm "$@"
    fi
}
k9s() {
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
    printf '\n🏠 kcm: session expired; switched to local. Select a context with kcm.\n' >&2
    return 1
}
