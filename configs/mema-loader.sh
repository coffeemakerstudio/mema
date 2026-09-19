# /etc/profile.d/mema.sh
# Mema environment loader

export MEMA_CONF_PATH="/opt/mema/config.d"
# Runtime package managers install user tools outside /usr/local/bin. Keep
# their standard locations reachable after a fresh login without shell-local
# exports.
if [ -n "${HOME:-}" ]; then
    for user_bin in "$HOME/go/bin" "$HOME/.bun/bin"; do
        case ":${PATH:-}:" in
            *:"$user_bin":*) ;;
            *) PATH="$user_bin${PATH:+:$PATH}" ;;
        esac
    done
    export PATH
fi
if [ -d "$MEMA_CONF_PATH" ]; then
    for file in "$MEMA_CONF_PATH"/*.sh; do
		[ -f "$file" ] && [ -r "$file" ] && . "$file"
    done
fi

