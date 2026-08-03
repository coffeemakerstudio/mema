curl -fsSL https://go.dev/dl/go1.26.3.linux-amd64.tar.gz -o https://go.dev/dl/go1.26.3.linux-amd64.tar.gz
sha256sum https://go.dev/dl/go1.26.3.linux-amd64.tar.gz 2b2cfc7148493da5e73981bffbf3353af381d5f93e789c82c79aff64962eb556
tar -C /opt/mema/go/1.26.3 -xzf go1.26.3.linux-amd64.tar.gz
ln -s /opt/mema/go/1.26.3/bin/go /usr/local/bin/go
