---
title: "clawker firewall add"
---

## clawker firewall add

Add an egress rule

### Synopsis

Add a domain to the firewall allow list. The rule takes effect immediately
via hot-reload — no container restart required.

Pass --path together with --action to add a path-scoped rule onto the domain
entry instead of (or alongside) the bare-domain allow. Path rules accumulate
across calls; a repeated --path with a different --action overwrites the
prior action for that path.

Pass --methods to narrow a path rule to a set of HTTP request methods (e.g.
GET,HEAD). The path rule's --action then applies only to those methods; other
methods fall through to later rules / the path default. Empty = all methods.
HTTP-family protos only (https/http/ws/wss).

A --path is a literal prefix by default, so --path /repos/x also matches
/repos/x-evil. Prefix the path with ~ to match it as a regex instead, which is
anchored end-to-end for exact matching (e.g. ~/repos/(a|b)/? matches only those
two repos, with or without a trailing slash). Quote regex paths — the shell
expands ~/ and treats ( | ? as special.

```
clawker firewall add <domain> [flags]
```

### Examples

```
  # Allow HTTPS traffic to a domain
  clawker firewall add registry.npmjs.org

  # Allow SSH traffic on a custom port
  clawker firewall add git.example.com --proto ssh --port 22

  # Allow plain TCP traffic
  clawker firewall add api.example.com --proto tcp --port 8080

  # Add a path-scoped allow rule onto a domain entry
  clawker firewall add api.example.com --path /v1 --action allow

  # Make a host read-only: allow GET/HEAD on all paths, deny the rest
  clawker firewall add api.github.com --path / --action allow --methods GET,HEAD

  # Deny mutating methods on a path prefix (reads still fall through)
  clawker firewall add api.github.com --path /repos/ --action deny --methods POST,PUT,PATCH,DELETE

  # Allow only two repos exactly (regex, anchored) — blocks /repos/clawker-evil
  clawker firewall add api.github.com --path '~/repos/(clawker|anthropic)/?' --action allow
```

### Options

```
      --action string     Action for the path rule: allow or deny (requires --path)
  -h, --help              help for add
      --methods strings   HTTP methods the path rule applies to (e.g. GET,HEAD); empty = all methods. Requires --path/--action; https/http/ws/wss only
      --path string       URL path for a path-scoped rule: a literal prefix (e.g. /v1), or an RE2 regex if prefixed with ~ for exact matching (e.g. ~/repos/(a|b)/?); requires --action
      --port string       Destination port: a single port (443) or an inclusive range (9000-9100); default: protocol-specific
      --proto string      Protocol: https (default), http, ssh, tcp, or any opaque protocol name (default "https")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
