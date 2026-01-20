# Host Proxy IPv6 Fix - Completed (CORRECTED)

## Date: 2026-01-19

## Issue
After the firewall IPv6 fix (commit `45b4a9d`), the following stopped working:
1. Claude Code no longer opens the host browser automatically (`host-open`)
2. Git pull with SSH fails with "Permission denied (publickey)" (`ssh-agent-proxy`)

## Root Cause Analysis

### Initial Theory (INCORRECT)
The initial theory was that the host proxy needed to listen on IPv6 `[::1]` in addition to IPv4 `127.0.0.1`. This was implemented but **did not fix the issue**.

### Actual Root Cause
The firewall script (`init-firewall.sh`) was using `getent hosts host.docker.internal` which **only returns one address** (the IPv6 address on Docker Desktop):

```bash
$ getent hosts host.docker.internal
fdc4:f303:9324::254 host.docker.internal  # IPv6 only!
```

The IPv4 address (`192.168.65.254`) was NOT being returned, so it was NOT being allowed through the firewall. The IPv4 address is only available via:

```bash
$ getent ahostsv4 host.docker.internal
192.168.65.254  STREAM host.docker.internal
```

### Why it worked before the firewall fix
Before commit `45b4a9d`:
- IPv6 was blocked by the firewall
- curl fell back to IPv4
- IPv4 happened to be allowed via the `HOST_NETWORK` rule (172.x.x.x range)
- Connection worked

After the firewall fix:
- IPv6 was allowed but only the IPv6 address was in the firewall rules
- IPv4 address `192.168.65.254` was NOT in the firewall rules
- curl tried IPv6 first (worked through firewall) but host proxy wasn't listening on the right IPv6
- curl tried IPv4 but firewall blocked it with "No route to host"

## Fix Applied

Modified `pkg/build/templates/init-firewall.sh` to use both `getent hosts` AND `getent ahostsv4`:

```bash
# Before (only got IPv6):
host_addrs=$(getent hosts host.docker.internal 2>/dev/null | awk '{print $1}')

# After (gets both IPv4 and IPv6):
host_addrs=$( (getent hosts host.docker.internal 2>/dev/null | awk '{print $1}'; getent ahostsv4 host.docker.internal 2>/dev/null | awk '{print $1}') | sort -u )
```

### Server.go IPv6 Change
The earlier change to `internal/hostproxy/server.go` to listen on both `127.0.0.1` and `[::1]` is harmless but **was not the fix**. Docker Desktop routes `host.docker.internal` traffic to the host's loopback interface (`127.0.0.1`), so listening on loopback only is correct.

## Verification

Tested with container running:
1. Firewall now shows both addresses being allowed:
   - `Allowing host.docker.internal (IPv4): 192.168.65.254`
   - `Allowing host.docker.internal (IPv6): fdc4:f303:9324::254`
2. `curl http://host.docker.internal:18374/health` from container succeeds
3. Both loopback-only (`127.0.0.1`) and all-interfaces (`0.0.0.0`) servers work

## Status
- ✅ Firewall script fix applied
- ✅ All tests pass (`go test ./...`)
- ✅ Container connectivity verified
- ✅ Ready for user to rebuild and test full functionality

## Files Changed
- `pkg/build/templates/init-firewall.sh` (the actual fix)
- `internal/hostproxy/server.go` (earlier change, harmless but not the fix)