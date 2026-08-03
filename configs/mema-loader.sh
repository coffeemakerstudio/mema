# /etc/profile.d/mema.sh
# Mema environment loader

export MEMA_CONF_PATH="/opt/mema/config.d"
if [ -d "$MEMA_CONF_PATH" ]; then
    for file in "$MEMA_CONF_PATH"/*.sh; do
		[ -f "$file" ] && [ -r "$file" ] && . "$file"
    done
fi

