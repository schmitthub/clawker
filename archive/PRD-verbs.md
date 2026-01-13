# Enhancement: update verbs

## Problem Statement

Clawker currently supports too many command verbs leading to a confusing and poor user experience.

## Goal

The goal is to consolidate verbs by grouping related actions making UX less confusing and feel more seemless because Clawker will have two main user types:

1. Vibe Coders: Users new to software development and its tooling who will be more apt to undertsand very common verbs
2. Experienced Coders: Users who are very experienced with software development and used to esoteric verbs used in low levels tools

The assumption right now is the majority of users will most likely fall in the first group as `clawker` is an abstraction that simplifies creating and managing containers. `docker` experienced users will be less apt to pursue a solution like this, but may want to adopt for other convenience features like monitoring etc.

**`clawker` should prioritize being intuitive for those new to container management and just want to intuitively run claude code, but do its best to also make docker users feel right at home whenever possible**

## Implementation Plan: Identify actions into most the most common generic verbs and consolidate existing command verbs into the common verb as a command. Add alternative more specific verbs that are less generic, or commonly used in other cli tools, as aliases

**note:** Explore other opportunities to define re-used actions across existin verbs to consolidate on during planning. Docker-cli commands should inspire some of the verb or alias choices due to its wide adoption and relation to `clawker`.

### Action: Run

Clawker runs containers, and currently features two command verbs `run` and `start`, with both having the same end action of running a container. "Start" as a verb is more associated with long-running actions like a service, so "run" is the more generic/generalized verb option. Additionally have `sh` or `shell` as yet another verb associated with running containers we should remove it. To simplify that action we will consolidate `shell` `start` and `run` by:

* Removing `start` as a command and instead aliasing it to `run` as some users, especially those familiar with docker compose, might choose that verb intuitively
* Remove `shell` | `sh` command.
* The `run` command will be refactored to indirectly or directly support what `start` and `shell` used to offer:
  * `run` by default will start a container and create bind mounted volumes to the workspace directory (cwd), and new volumes to persist its command history and claude home directories (ie: "~/.claude")
  * Add optional flag `-r --remove`: starts an ephemeral container with all new volumes destroyed after it stops
  * add `-sh --shell` as a convenience flag to pass a shell or use a default shell
  * @/pkg/build/templates/entrypoint.sh should prepend the command `claude` if the first argument passed to the container starts with "-" or isn't a command and be set as container entrypoints. Generated dockerfiles should not set a CMD so that it is easier for users to treat `run` as either a direct claude instance, or pass in a new command to bypass the `claude` command and do something else

### Action: Remove

Clawker removes container resources: `containers`, `images`, `volumes`, `networks`. But it features two verbs: `remove` and `prune`. `Prune` is a far less common verb, but borrowed from `docker` cli due to user's potential famliarity with it. But, our main audience, Group 1 aka "Vibe Coders", will have no idea what dangling resources are and could quickly fill up their hard drive with old images, volumes, etc.

Therefore we should:

* Merge `prune` into `remove` as an alias.
* Plan a way to automatically clean up user's dangling resources by default while also exposing a way that an advanced user can disable it
