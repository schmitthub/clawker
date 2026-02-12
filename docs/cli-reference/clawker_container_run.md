## clawker container run

Create and run a new container

### Synopsis

Create and run a new clawker container from the specified image.

Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml.

If IMAGE is "@", clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag

```
clawker container run [OPTIONS] IMAGE [COMMAND] [ARG...] [flags]
```

### Examples

```
  # Run an interactive shell
  clawker container run -it --agent shell @ alpine sh

  # Run using default image with generated agent name from config
  clawker container run -it @

  # Run a command
  clawker container run --agent worker @ echo "hello world"
  clawker container run --agent worker myimage:tag echo "hello world"

  # Pass a claude code flag
  clawker container run --detach --agent web @ -p "build entire app, don't make mistakes"

  # Run with environment variables
  clawker container run -it --agent dev -e NODE_ENV=development @ echo $NODE_ENV

  # Run with a bind mount
  clawker container run -it --agent dev -v /host/path:/container/path @

  # Run and automatically remove on exit
  clawker container run --rm -it @ sh
```

### Options

```
      --add-host stringArray                Add custom host-to-IP mapping (host:ip)
      --agent string                        Agent name for container (uses clawker.<project>.<agent> naming)
      --annotation map                      Add an annotation to the container (passed through to the OCI runtime)
  -a, --attach list                         Attach to STDIN, STDOUT or STDERR
      --blkio-weight uint16                 Block IO (relative weight), between 10 and 1000, or 0 to disable
      --blkio-weight-device weight-device   Block IO weight (relative device weight)
      --cap-add stringArray                 Add Linux capabilities
      --cap-drop stringArray                Drop Linux capabilities
      --cgroup-parent string                Optional parent cgroup for the container
      --cgroupns string                     Cgroup namespace to use (host|private)
      --cidfile string                      Write the container ID to the file
      --cpu-count int                       CPU count (Windows only)
      --cpu-percent int                     CPU percent (Windows only)
      --cpu-period int                      Limit CPU CFS (Completely Fair Scheduler) period
      --cpu-quota int                       Limit CPU CFS (Completely Fair Scheduler) quota
      --cpu-rt-period int                   Limit CPU real-time period in microseconds
      --cpu-rt-runtime int                  Limit CPU real-time runtime in microseconds
  -c, --cpu-shares int                      CPU shares (relative weight)
      --cpus decimal                        Number of CPUs (e.g., 1.5)
      --cpuset-cpus string                  CPUs in which to allow execution (0-3, 0,1)
      --cpuset-mems string                  MEMs in which to allow execution (0-3, 0,1)
      --detach                              Run container in background and print container ID
      --device device                       Add a host device to the container
      --device-cgroup-rule stringArray      Add a rule to the cgroup allowed devices list
      --device-read-bps throttle-device     Limit read rate (bytes per second) from a device
      --device-read-iops throttle-device    Limit read rate (IO per second) from a device
      --device-write-bps throttle-device    Limit write rate (bytes per second) to a device
      --device-write-iops throttle-device   Limit write rate (IO per second) to a device
      --dns stringArray                     Set custom DNS servers
      --dns-option stringArray              Set DNS options
      --dns-search stringArray              Set custom DNS search domains
      --domainname string                   Container NIS domain name
      --entrypoint string                   Overwrite the default ENTRYPOINT
  -e, --env stringArray                     Set environment variables
      --env-file stringArray                Read in a file of environment variables
      --expose stringArray                  Expose a port or a range of ports
      --gpus gpu-request                    GPU devices to add to the container ('all' to pass all GPUs)
      --group-add stringArray               Add additional groups to join
      --health-cmd string                   Command to run to check health
      --health-interval duration            Time between running the check (e.g., 30s, 1m)
      --health-retries int                  Consecutive failures needed to report unhealthy
      --health-start-interval duration      Time between running the check during the start period
      --health-start-period duration        Start period for the container to initialize (e.g., 5s)
      --health-timeout duration             Maximum time to allow one check to run (e.g., 30s)
  -h, --help                                help for run
      --hostname string                     Container hostname
      --init                                Run an init inside the container that forwards signals and reaps processes
  -i, --interactive                         Keep STDIN open even if not attached
      --io-maxbandwidth bytes               Maximum IO bandwidth limit for the system drive (Windows only)
      --io-maxiops uint                     Maximum IOps limit for the system drive (Windows only)
      --ip string                           IPv4 address (e.g., 172.30.100.104)
      --ip6 string                          IPv6 address (e.g., 2001:db8::33)
      --ipc string                          IPC mode to use
      --isolation string                    Container isolation technology
  -l, --label stringArray                   Set metadata on container
      --label-file stringArray              Read in a file of labels
      --link stringArray                    Add link to another container
      --link-local-ip stringArray           Container IPv4/IPv6 link-local addresses
      --log-driver string                   Logging driver for the container
      --log-opt stringArray                 Log driver options
      --mac-address string                  Container MAC address (e.g., 92:d0:c6:0a:29:33)
  -m, --memory bytes                        Memory limit (e.g., 512m, 2g)
      --memory-reservation bytes            Memory soft limit
      --memory-swap bytes                   Total memory (memory + swap), -1 for unlimited swap
      --memory-swappiness int               Tune container memory swappiness (0 to 100) (default -1)
      --mode string                         Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy)
      --mount mount                         Attach a filesystem mount to the container
      --name string                         Same as --agent; provided for Docker CLI familiarity (mutually exclusive with --agent)
      --network network                     Connect a container to a network
      --network-alias stringArray           Add network-scoped alias for the container
      --no-healthcheck                      Disable any container-specified HEALTHCHECK
      --oom-kill-disable                    Disable OOM Killer
      --oom-score-adj int                   Tune host's OOM preferences (-1000 to 1000)
      --pid string                          PID namespace to use
      --pids-limit int                      Tune container pids limit (set -1 for unlimited)
      --privileged                          Give extended privileges to this container
  -p, --publish port                        Publish container port(s) to host
  -P, --publish-all                         Publish all exposed ports to random ports
      --read-only                           Mount the container's root filesystem as read only
      --restart string                      Restart policy (no, always, on-failure[:max-retries], unless-stopped)
      --rm                                  Automatically remove container when it exits
      --runtime string                      Runtime to use for this container
      --security-opt stringArray            Security options
      --shm-size bytes                      Size of /dev/shm
      --stop-signal string                  Signal to stop the container
      --stop-timeout int                    Timeout (in seconds) to stop a container
      --storage-opt stringArray             Storage driver options for the container
      --sysctl map                          Sysctl options
      --tmpfs stringArray                   Mount a tmpfs directory (e.g., /tmp:rw,size=64m)
  -t, --tty                                 Allocate a pseudo-TTY
      --ulimit ulimit                       Ulimit options
  -u, --user string                         Username or UID
      --userns string                       User namespace to use
      --uts string                          UTS namespace to use
  -v, --volume stringArray                  Bind mount a volume
      --volume-driver string                Optional volume driver for the container
      --volumes-from stringArray            Mount volumes from the specified container(s)
      --worktree string                     Use git worktree: 'branch' to use/create, 'branch:base' to create from base
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
