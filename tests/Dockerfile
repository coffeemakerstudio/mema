FROM debian:bookworm-slim

COPY dist /opt/local-repo

RUN if [ -f /opt/local-repo/InRelease ]; then \
        mkdir -p /etc/apt/keyrings && \
        cp /opt/local-repo/mema-keyring.gpg /etc/apt/keyrings/mema.gpg && \
        echo "deb [signed-by=/etc/apt/keyrings/mema.gpg] file:/opt/local-repo ./" > /etc/apt/sources.list.d/mema.list; \
    else \
        echo "deb [trusted=yes] file:/opt/local-repo ./" > /etc/apt/sources.list.d/mema.list; \
    fi

RUN apt-get update && \
    apt-get install -y --no-install-recommends mema-go-latest && \
    rm -rf /var/lib/apt/lists/*

RUN echo 'package main; import "fmt"; func main() { fmt.Println("Mema Smoke Test: OK") }' > /tmp/hello.go && \
    /usr/local/bin/go run /tmp/hello.go && \
    rm /tmp/hello.go && \
    mema list && \
    test -L /usr/local/bin/go
