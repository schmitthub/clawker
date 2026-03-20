---
title: "clawker firewall rotate-ca"
---

## clawker firewall rotate-ca

Rotate the firewall CA certificate

### Synopsis

Regenerate the CA keypair and all domain certificates used for TLS
inspection. Running containers will need to be rebuilt and recreated
to pick up the new CA.

```
clawker firewall rotate-ca [flags]
```

### Examples

```
  # Rotate the CA certificate
  clawker firewall rotate-ca
```

### Options

```
  -h, --help   help for rotate-ca
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
