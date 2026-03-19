# Firewall E2E Tests and Bug fixes Session (2026-03-18)

**Branch:** `feat/global-firewall`

## Bugs

- [ ] **Bypass incomplete**: Adding the bypass script is not finished or passing tests
- [x] **Stack shutsdown unexpectedly**: The daemon is not properly tracking clawker container uptimes like hostproxy does. It shuts down the envoy and coredns containers even though clawker containers are running
- [ ] **Rules not tested**: We have not tested config firewall.rules only config firewall.add_domain
- [ ] **CA Certs wrong dir**: CA certs are not being created in the certs directory, they are being created alongside it
- [ ] **Container init script says hostproxy is starting when its the firewall being waited on**
