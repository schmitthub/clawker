---
title: "clawker auth rotate"
---

## clawker auth rotate

Rotate control plane auth material

### Synopsis

Check and rotate authentication material for the control plane.

Without --force, checks all auth files and creates any that are missing.
Existing valid material is not modified (idempotent).

With --force, regenerates the CA certificate, server certificate, and
signing key. The CP must be restarted to pick up new material.

Auth material:
  - CA certificate and key (signs server and client certs, 5-year validity)
  - CLI signing key and JWK (ES256 for OAuth2 private_key_jwt auth)
  - Server TLS certificate and key (signed by CLI CA, 1-year validity)
  - Client mTLS certificate and key (signed by CLI CA, 1-year validity)

Private keys are always created with 0600 permissions.

```
clawker auth rotate [flags]
```

### Examples

```
  # Check auth material and create any missing files
  clawker auth rotate

  # Force-regenerate all auth material
  clawker auth rotate --force
```

### Options

```
      --force   Regenerate all auth material even if valid
  -h, --help    help for rotate
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker auth](clawker_auth) - Manage control plane authentication material
