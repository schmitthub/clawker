# Bug / Feature Tracker


## CP initiative 

- [ ] Clawker firewall up / clawker controlplane up fails if CP image isn't built yet. needs to handle building and starting the cp image along with better helper instructions. Seems to have resolved on a second attempt but should be looked into. 
```
clawker firewall up
Error: Image 'clawker-controlplane:latest' not found

Next Steps:
  1. Check the image name and tag are correct
  2. Verify you have network access to the registry
  3. Try pulling manually: docker pull clawker-controlplane:latest
```
- [ ] `clawker controlplane up`/`clawker firewall up` give no status output, no progress spinner. just hangs until a success message. bad ux
- [ ] `clawker firewall up` fails with `✗ starting firewall: rpc error: code = Internal desc = firewall init: firewall stack: envoy: creating container clawker-envoy: Failed to create container: Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /root/.local/share/clawker/firewall/envoy.yaml`
- [ ] Not using `internal/docker` client in `cmd/clawker-cp/main.go` 
- [ ] `cmd/clawker-cp/main.go` should probably be a light wrapper around `internal/controlplane` package instead of doing all the work itself.
- [ ] `clawker firewall up` doesn't need to display the stack ip and network id (its not even the name lol its a long random id). its totally useless info to the user. 
- [ ] CP should remove its container on shutdown imo. unless there is some functional benefit or constraint in doing so

## General

- [ ] Clawker share dir should be overridable via env var and settings 
- [ ] Socketbridge deamon log files need to be rotated and cleaned up somehow. It is also difficult to really track down which daemon log is the most recent one visually especially when hundreds of log files are generated. Aggregating logs into a single file with timestamps and log levels would be ideal.
- [ ] Egress monitoring for the firewall stack 
- [ ] Might be nice to have all logging also aggregated in monitoring stack  
- [ ] **Transparent TCP for random ports not working** — TLS on non-443, HTTP on non-80 (e.g. `tls://host:4443`, `http://host:8080`) doesn't route through Envoy. Needs Envoy config update. (Migrated from `firewall-egress-stack-e2e-bugs`; may have been addressed by eBPF pivot on `fix/project-egress-priority` — re-verify against current `internal/firewall/envoy.go` before triaging.)
- [ ] **HTTP over raw IP (no domain) never tested** — unclear whether it's even implemented. (Migrated from `firewall-egress-stack-e2e-bugs`; same re-verify note as above.)


