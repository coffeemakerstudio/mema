FROM debian:bookworm
RUN apt-get update && apt-get install -y --no-install-recommends curl gpg gpg-agent python3 sqlite3 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY core/mema /usr/local/bin/mema
COPY tests/manage_qualification/service.py /opt/mema-qualification/bin/service.py
COPY tests/manage_qualification/qualification.service /etc/systemd/system/mema-qualification.service
COPY tests/manage_qualification/nginx.conf /etc/nginx/sites-available/mema-qualification
COPY tests/manage_qualification/manifest.json /etc/mema/manage/qualification.json
RUN printf '#!/bin/sh\nexit 0\n' > /usr/local/bin/systemctl && printf '#!/bin/sh\nexit 0\n' > /usr/local/bin/nginx && chmod 0755 /usr/local/bin/systemctl /usr/local/bin/nginx && \
    chmod 0755 /usr/local/bin/mema /opt/mema-qualification/bin/service.py && \
    mkdir -p /etc/mema/manage /etc/mema-qualification /etc/nginx/sites-enabled /var/lib/mema-qualification/data /var/lib/mema-qualification/identity && \
    ln -s /etc/nginx/sites-available/mema-qualification /etc/nginx/sites-enabled/mema-qualification && \
    ln -s /var/lib/mema-qualification/data /var/lib/mema-qualification/current && \
    printf 'QUAL_DB=/var/lib/mema-qualification/app.db\nQUAL_VERSION=1.0.0\nQUAL_SECRET=secret-fixture-not-for-output\n' > /etc/mema-qualification/service.env && chmod 0600 /etc/mema-qualification/service.env && \
    printf 'persistent-data\n' > /var/lib/mema-qualification/data/persistent.txt && \
    printf 'identity-marker\n' > /var/lib/mema-qualification/identity/marker && \
    sqlite3 /var/lib/mema-qualification/app.db 'create table fixture(id integer primary key,value text); insert into fixture values(1,"sqlite-record");' && chmod 0600 /var/lib/mema-qualification/app.db
