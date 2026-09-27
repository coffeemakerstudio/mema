FROM debian:trixie
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d \
 && chmod 0755 /usr/sbin/policy-rc.d \
 && apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv systemd-container dbus dbus-user-session gpg gpg-agent python3 nginx curl ca-certificates util-linux \
 && rm -f /usr/sbin/policy-rc.d \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 1000 --shell /bin/bash eugen
COPY core/mema /usr/local/bin/mema
RUN chmod 0755 /usr/local/bin/mema
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
