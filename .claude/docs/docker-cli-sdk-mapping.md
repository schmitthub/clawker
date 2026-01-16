# Docker CLI to Go SDK Mapping Reference

> **LLM Memory Document**: Comprehensive mapping of Docker CLI commands to their Go SDK equivalents for the clawker architecture migration.

## Overview

This document maps Docker CLI commands to their corresponding Go SDK methods from `github.com/docker/docker/client`. Use this reference when implementing Docker functionality in Go.

**SDK Package**: `github.com/docker/docker/client`

---

## Command Alias Resolution

Docker CLI has many aliases. The **canonical form** is the full management command path. This table shows how top-level shortcuts map to their true commands.

| Shortcut | Canonical Command | Notes |
|----------|-------------------|-------|
| `docker ps` | `docker container list` | Also: `docker container ls`, `docker container ps` |
| `docker run` | `docker container run` | Compound: create + start |
| `docker exec` | `docker container exec` | |
| `docker attach` | `docker container attach` | |
| `docker cp` | `docker container cp` | |
| `docker create` | `docker container create` | |
| `docker kill` | `docker container kill` | |
| `docker logs` | `docker container logs` | |
| `docker pause` | `docker container pause` | |
| `docker port` | `docker container port` | |
| `docker rename` | `docker container rename` | |
| `docker restart` | `docker container restart` | |
| `docker rm` | `docker container remove` | Also: `docker container rm` |
| `docker start` | `docker container start` | |
| `docker stats` | `docker container stats` | |
| `docker stop` | `docker container stop` | |
| `docker top` | `docker container top` | |
| `docker unpause` | `docker container unpause` | |
| `docker update` | `docker container update` | |
| `docker wait` | `docker container wait` | |
| `docker build` | `docker image build` | Also: `docker buildx build` |
| `docker images` | `docker image list` | Also: `docker image ls` |
| `docker rmi` | `docker image remove` | Also: `docker image rm` |

---

## Container Commands

### Command Reference Table

| CLI Command | SDK Method | Key Options Type |
|-------------|------------|------------------|
| `container create` | `ContainerCreate()` | `container.Config`, `container.HostConfig`, `network.NetworkingConfig` |
| `container start` | `ContainerStart()` | `container.StartOptions` |
| `container stop` | `ContainerStop()` | `container.StopOptions` |
| `container kill` | `ContainerKill()` | signal string |
| `container rm` | `ContainerRemove()` | `container.RemoveOptions` |
| `container list` | `ContainerList()` | `container.ListOptions` |
| `container inspect` | `ContainerInspect()` | - |
| `container logs` | `ContainerLogs()` | `container.LogsOptions` |
| `container attach` | `ContainerAttach()` | `container.AttachOptions` |
| `container exec` | `ContainerExecCreate()` + `ContainerExecAttach()` | `container.ExecOptions`, `container.ExecStartOptions` |
| `container pause` | `ContainerPause()` | - |
| `container unpause` | `ContainerUnpause()` | - |
| `container restart` | `ContainerRestart()` | `container.StopOptions` |
| `container rename` | `ContainerRename()` | newName string |
| `container top` | `ContainerTop()` | ps arguments |
| `container stats` | `ContainerStats()` | stream bool |
| `container update` | `ContainerUpdate()` | `container.UpdateConfig` |
| `container wait` | `ContainerWait()` | `container.WaitCondition` |
| `container cp` (to) | `CopyToContainer()` | `container.CopyToContainerOptions` |
| `container cp` (from) | `CopyFromContainer()` | - |
| `container port` | `ContainerInspect()` | Extract from NetworkSettings |

---

## Image Commands

### Command Reference Table

| CLI Command | SDK Method | Key Options Type |
|-------------|------------|------------------|
| `image build` | `ImageBuild()` | `types.ImageBuildOptions` |
| `image list` | `ImageList()` | `image.ListOptions` |
| `image remove` | `ImageRemove()` | `image.RemoveOptions` |
| `image pull` | `ImagePull()` | `image.PullOptions` |
| `image push` | `ImagePush()` | `image.PushOptions` |
| `image tag` | `ImageTag()` | - |
| `image inspect` | `ImageInspectWithRaw()` | - |
| `image history` | `ImageHistory()` | - |
| `image save` | `ImageSave()` | - |
| `image load` | `ImageLoad()` | - |
| `image prune` | `ImagesPrune()` | `filters.Args` |

---

## Network Commands

### Command Reference Table

| CLI Command | SDK Method | Key Options Type |
|-------------|------------|------------------|
| `network create` | `NetworkCreate()` | `network.CreateOptions` |
| `network rm` | `NetworkRemove()` | - |
| `network ls` | `NetworkList()` | `network.ListOptions` |
| `network inspect` | `NetworkInspect()` | `network.InspectOptions` |
| `network connect` | `NetworkConnect()` | `network.EndpointSettings` |
| `network disconnect` | `NetworkDisconnect()` | force bool |
| `network prune` | `NetworksPrune()` | `filters.Args` |

---

## Volume Commands

### Command Reference Table

| CLI Command | SDK Method | Key Options Type |
|-------------|------------|------------------|
| `volume create` | `VolumeCreate()` | `volume.CreateOptions` |
| `volume rm` | `VolumeRemove()` | force bool |
| `volume ls` | `VolumeList()` | `volume.ListOptions` |
| `volume inspect` | `VolumeInspect()` | - |
| `volume prune` | `VolumesPrune()` | `filters.Args` |

---

## Global Options

Docker CLI global options map to client configuration:

| CLI Option | SDK Configuration |
|------------|-------------------|
| `-H, --host` | `client.WithHost(host)` |
