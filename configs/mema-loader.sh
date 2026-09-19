# /etc/profile.d/mema.sh
# Mema environment loader

export MEMA_CONF_PATH="/opt/mema/config.d"
# Go installs user tools into GOPATH/bin by default. Keep that standard
# developer location reachable after a fresh login without shell-local exports.
if [ -n "${HOME:-}" ]; then
    case ":${PATH:-}:" in
        *:"$HOME/go/bin":*) ;;
        *) PATH="$HOME/go/bin${PATH:+:$PATH}"; export PATH ;;
    esac
fi
if [ -d "$MEMA_CONF_PATH" ]; then
    for file in "$MEMA_CONF_PATH"/*.sh; do
		[ -f "$file" ] && [ -r "$file" ] && . "$file"
    done
fi

