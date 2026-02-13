# HTML Preview Workflow (In-Container Browser Preview)

## Problem
When working inside a clawker container, you need to preview HTML files in the host browser.
The container has `host-open` (BROWSER env) which sends URLs to the host proxy, but:
- `file://` URLs are rejected by the host proxy (only http/https allowed)
- Container ports are not published to the host by default
- `data:` URIs are too large for the host-open script

## Solution: Python HTTP Server + Docker Relay Container

### Step 1: Serve files inside the container

```bash
# Create a directory for preview files
mkdir -p /tmp/callbacks

# Copy/generate HTML files there
cp /tmp/my-page.html /tmp/callbacks/

# Start a simple HTTP server (directory listing enabled)
python3 -m http.server 19876 --directory /tmp/callbacks --bind 0.0.0.0 > /dev/null 2>&1 &
echo "PID=$!"
```

### Step 2: User creates relay container on the host

The agent cannot do this step — the user must run this from a **host terminal**.
The relay container must be on the same Docker network as the clawker container (typically `clawker-net`).

```bash
docker run --rm -d --name preview-relay \
  -p 19876:19876 \
  --network clawker-net \
  alpine/socat TCP-LISTEN:19876,fork,reuseaddr TCP:<container_hostname_or_id>:19876
```

The container hostname can be found inside the container with `hostname`.
After this, `http://localhost:19876/` is browsable from the host.

### Step 3: Iterate on design

Regenerate files into `/tmp/callbacks/`, user refreshes browser.
No need to restart the server — python's SimpleHTTPServer serves fresh files on each request.

### Step 4: Cleanup

Inside container:
```bash
kill <server_pid>
rm -rf /tmp/callbacks
```

User on host:
```bash
docker rm -f preview-relay
```

## Key Learnings
- `host-open` works by POSTing to the host proxy at `$CLAWKER_HOST_PROXY/open/url`, which calls `open`/`xdg-open` on the host. The URL must be reachable from the host.
- Container ports aren't accessible from the host without a relay or published ports.
- `--network clawker-net` is required for the relay to reach the clawker container.
- `-p` and `--network container:` are mutually exclusive in Docker, so a relay on the same user-defined network is the right approach.
