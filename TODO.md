# TODO.md

[ ] Add user scoped default templates and configs in their claucker home dir
[ ] Multi image support. you can create as many or as little claucker containers as you want
[ ] Add progress bars with status updates to CLI output instead of verbose logs endless log entries in the terminal
[ ] Add session tracking by container name in metrics collection, explained in metrics docs
[ ] Make an SRE/Devops specialist sub-agent to troubelshoot container and service issues because it destroys the context
  window

## Bugs

[ ] monitor start existing container warning command prints out the path of everything instead of simply `claucker restart`.
  It looks like Claude is trying to detect all running containers within the `claucker-net` network. this can probably be addresssed when multi-image support is added

```shell
⚠️  Running containers detected without telemetry:
   • monitor-loki-1
   • monitor-otel-collector-1
   • monitor-prometheus-1
   • monitor-grafana-1
   • monitor-jaeger-1
   • claucker-workspace

These containers were started before the monitoring stack and
won't export telemetry. To enable telemetry, restart them:

cd /path/to/monitor-loki-1 && claucker restart
cd /path/to/monitor-otel-collector-1 && claucker restart
cd /path/to/monitor-prometheus-1 && claucker restart
cd /path/to/monitor-grafana-1 && claucker restart
cd /path/to/monitor-jaeger-1 && claucker restart
cd /path/to/workspace && claucker restart
```
