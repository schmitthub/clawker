---
title: "clawker bundle install"
---

## clawker bundle install

Declare a bundle source and fetch its content

### Synopsis

Declares a bundle source in a clawker.yaml 'bundles:' entry and fetches its
content into the host cache.

The source is a git clone URL (https or ssh), an owner/repo GitHub shorthand
(expanded to a URL before writing), or a local directory path (loaded in place,
the dev loop). A remote source may pin --ref or --sha; unpinned tracks the
repository's default branch. With no source, declared-but-uncached bundles are
fetched.

By default the entry is written to the user config-dir clawker.yaml; --project
writes the project clawker.yaml and --local the uncommitted project override.

```
clawker bundle install [source] [flags]
```

### Examples

```
  # Install from a git URL pinned to a tag
  clawker bundle install https://github.com/acme/tools.git --ref v1.2.0

  # Unpinned — tracks the repository's default branch
  clawker bundle install https://github.com/acme/extras.git

  # GitHub owner/repo shorthand, into the project config
  clawker bundle install acme/tools --sha <40-hex> --project

  # A local directory (dev loop)
  clawker bundle install ./vendor/my-bundle --project
```

### Options

```
      --auto-update     Refetch this bundle when its source version changes
  -h, --help            help for install
      --local           Write to the uncommitted project clawker.local.yaml
      --project         Write to the project clawker.yaml
      --ref string      Branch or tag to fetch from a remote source
      --sha string      Full 40-character commit SHA to pin a remote source
      --subdir string   Repository subdirectory holding the bundle (monorepo)
      --user            Write to the user config-dir clawker.yaml (default)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker bundle](clawker_bundle) - Manage distributed bundles of harnesses, stacks, and monitoring extensions
