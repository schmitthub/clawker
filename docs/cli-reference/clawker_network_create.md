## clawker network create

Create a network

### Synopsis

Creates a new clawker-managed network.

The network will be labeled as a clawker-managed resource.
By default, a bridge network driver is used.

```
clawker network create [OPTIONS] NETWORK [flags]
```

### Examples

```
  # Create a network
  clawker network create mynetwork

  # Create an internal network (no external connectivity)
  clawker network create --internal mynetwork

  # Create a network with custom driver options
  clawker network create --driver bridge --opt com.docker.network.bridge.name=mybridge mynetwork

  # Create a network with labels
  clawker network create --label env=test --label project=myapp mynetwork
```

### Options

```
      --attachable          Enable manual container attachment
      --driver string       Driver to manage the network (default "bridge")
  -h, --help                help for create
      --internal            Restrict external access to the network
      --ipv6                Enable IPv6 networking
      --label stringArray   Set metadata for a network
  -o, --opt stringArray     Set driver specific options
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker network](clawker_network.md) - Manage networks
