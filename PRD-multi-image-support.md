---

# Product Requirement Document: Claucker Multi Image Support

**Document Status:** `APPROVED`

**Version:** 1.0.0

**Architect:** Andrew Schmitt

**Date:** January 10, 2026

---

## 1. Executive Summary

This feature introduces support for management of multiple running or stopped containers. Claucker's benefits of container isolation, firewall, and obvservability makes it a perfect candidate for users to employ more advanced, creative, complex multi-agent workflows leveraging tactics like prompt interation loops, running claude code with `--dangerously-skip-permissions`, and using git worktrees for claude code to work on multiple features for a project at the same time.

## 2. Problem Statement

Users want to use more aggressive strategies when leveraging claude code to get more done, being able to easily command multiple claude containers and monitor them helps significantly.

The relationahip between project, image, and container is many-to-many and complex. As in many images can be associated with one project (ie monorepos with varying tool requirements), many containers can be running multiple claude code agents working on different features using one or more images, and one image can be used across any number of projects and containers

## 3. Goals & Non-Goals

### Goals

* Add claucker created container management commands and enhancements to the command line interface
* Maintain existing claucker single container management features, multiple container management should be in addition-to

### Non-Goals

* Overhauling claucker's existing single container management
* Management of containers not created by claucker

## 4. Functional Requirements

### 4.1 Grouping

Created containers names and associated resouces need to be grouped using "/" separators.

* The first part of the name, the prefix, should always be "claucker"
* The second part of the name should be the associated project name taken from `claucker.yaml` or fallback to the name of the current directory
* The last part of the name should be set using either an --agent-name flag if provided, or fallback to using dockers randomized names

ex `claucker start` creates a container and resources using name `claucker/projectName/random-names-here`
ex `clacker start --agent ralph` creates a container and resources using name `claucker/projectName/ralph`
ex `claucker run --agent ralph -- -p "write me a poem"` creates a container and resources using name `claucker/projectName/ralph`

### 4.2. CLI Commands

#### List Command

* **Command:** `claucker ls`
* **Description:** Lists all running claude code containers created by claucker
* **Behavior**
  * Default: Shows all running claude code containers
  * Flag -a|-all: Shows all running and stopped claude code containers
  * Flag -p|--project $name: filters by project name

#### Remove Command

* **Command:** `claucker rm -n $name`
* **Description:** Removes a give claucker container name r
* **Behavior**
  * flag -n|--name: removes a container by name
  * flag -p|--project: removes all containers for the project
  * All related container resources should be destroyed by this operation
