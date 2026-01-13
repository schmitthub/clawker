# TODO.md

## Personal

[ ] Get a good list of software development terms for prompt creating. You could prob make a good article. (ie enhancement, feature, integration test, regression test, hotfix, etc etc)

## Quality issues

[ ] pkg/cmd/run/run.go:75 The short flag -u is used by both run --user and remove --unused commands. While Cobra allows different subcommands to have the same short flags, this creates confusion for users and violates CLI consistency guidelines. Consider changing one of these short flags to avoid ambiguity. Recommendation: Use a different short flag for --unused (perhaps no short flag, or -U if uppercase is acceptable).
[ ] pkg/cmd/remove/remove.go The --unused --all functionality should include testing for image removal (not just volumes), as documented
  in the command's long description (lines 44-47 in remove.go). The integration tests only verify volume removal with TestRm_UnusedFlag_WithAll_RemovesVolumes but don't test that images are being removed as claimed in the documentation.
[ ] internal/config/loader.go The new environment variable binding functionality added to the config loader (lines 48-52) lacks test
  coverage. Specifically, there are no tests verifying that:

* The CLAWKER_prefix works correctly
* The SetEnvKeyReplacer properly converts dots to underscores
* Environment variables like CLAWKER_AGENT_SHELL correctly override config file values
* The AutomaticEnv() integration works as expected
  This is critical functionality for the shell path resolution feature and should have comprehensive tests.
[ ] The new utility functions PrintStatus and OutputJSON in pkg/cmdutil/output.go lack unit tests. These are public API functions that
  will be used across the codebase and should have test coverage verifying:

* PrintStatus respects the quiet flag
* OutputJSON produces valid, indented JSON
* Error cases are handled appropriately
  Consider adding tests in pkg/cmdutil/output_test.go
[ ]

## Bugs

[ ] Tests are a fucking mess so putting this under bugs. need to do it one cmd at a time so claude can focus
[ ] Terminal still locks when quitting out of the container when the CC initial setup page (auth, accept dir, etc) is active

## Enhancements

[ ] Update config template to have all vars in it, commented out. have a helper function describe the entire config schema
[ ] figure out what you're gunna do about seamlessly handling git creds :D, prob just copy mount ssh dir
[ ] I will need incorporate a mimic of docker attach for container shell session detachment and re-attachment. Right now we can leverage `run` with a --detach flag to start a claude command. Users might want to leave containers running a job and re-log into the same shell session later
[ ] Might also want to add exec too
[ ] Custom actions? let people create workflows to wrap container start finish?
[ ] add firewall config overrides to run command (flag, env var)
[ ] grafana subscription tracking support
[ ] leverage llm doc gen to describe this project to claude easily: <https://cobra.dev/docs/how-to-guides/clis-for-llms/>
[ ] add these cobra site docs to context ex: <https://github.com/spf13/cobra/blob/main/site/content/user_guide.md>
[ ] identify situations that could benefit from active help: <https://github.com/spf13/cobra/blob/main/site/content/active_help.md>
[ ] docker mcp toolkit integration
[ ] Add monitoring pre-check. Disable pre-checks in local yaml
[ ] Add modes: yolo and ralf, to start claude in unsafe auto or through a ralf entry script that accepts a prompt and num iterations. prob should add aliases to them as command verbs for fun (ie run --mode ralph aliased to clawker ralf). or have a modes subcommand like clawker modes ralph.
[ ] Make timezone in the dockerfile tmpl configurable in clawker.yaml or use the hosts default TZ
[ ] see if there is a way to pass browser opening events to the host and then back into the container. for example for claude auth, and for mcp's like serena
[ ] can you rename @pkg/build/ to something like pkg/package/ since it is simply creating dockerfiles  based on current claude code changes. could there be a better name to fit trends to fit this behavior better?
[ ] Claude file mounting from host strategy. Balancing convenience with sharing with host tradeoffs. So maybe two claude modes (shared (bind) vs fresh(do nothing) vs isolated (copy)) vs two workspace modes (shared (bind), vs isolated (copy))
[ ] ZSH install and oh my zsh should be an optional. Come to think of it have the recommended image you like to use and make all of those things optional so that most ppl can just hit the ground running
[ ] Add "include language" build options to make adding build tools for each languange easy
[ ] Add man pages like gh. Describe config file in detail etc
[ ] consider adding heredoc support to make multiline string literals format prettier in code
[ ] Config properties are confusing (ie "instructions" but actually is pure shell commands) Injection poitns should be all we use that take in proper docker build instructions
[ ] Go docs
[ ] github pages site w/ hugo mkdocs
[ ] Add a light monitoring shell UI that is aware of claude subs vs claude sdk api costs
[ ] Add progress bars with status updates to CLI output instead of verbose logs endless log entries in the terminal
[ ] Fix generate cmd output its too verbose
[ ] clawker output should mirror claude code ANSI style
[ ] plan a refactor that sets `CLAWKER_PROJECT` and `CLAWKER_AGENT` in two places and move it  "envBuilder.Set("CLAWKER_PROJECT", cfg.Project)" and "envBuilder.Set("CLAWKER_AGENT", agentName)" from @pkg/cmd/run/run.go and @pkg/cmd/start/start.go
[ ] makefile updates to remove calls to the legacy shell scripts; commands to run tests

## Release

[ ] Commit into new repo "clawker/clawker"

## Marketing

[ ] Make PRs in popular list repos like <https://github.com/hesreallyhim/awesome-claude-code>
