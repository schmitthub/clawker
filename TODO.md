# TODO.md

## Bugs

[ ] Terminal still locks when quitting out of the container when the CC initial setup page (auth, accept dir, etc) is active

## Enhancements

[ ] change name to clawker
[ ] update verbs. `run` with flag `-r --remove` should start a container. work on passthrough if someone wants a shell or abitrary command or claude flag passed. add `-sh --shell` as a convenience flag. remove `start` and `sh`
[ ] leverage llm doc gen to describe this project to claude easily: <https://cobra.dev/docs/how-to-guides/clis-for-llms/>
[ ] add these cobra site docs to context ex: <https://github.com/spf13/cobra/blob/main/site/content/user_guide.md>
[ ] identify situations that could benefit from active help: <https://github.com/spf13/cobra/blob/main/site/content/active_help.md>
[ ] docker mcp toolkit integration
[ ] Add monitoring pre-check. Disable pre-checks in local yaml
[ ] Add modes: yolo and ralf, to start claude in unsafe auto or through a ralf entry script that accepts a prompt and num iterations
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
[ ] maybe remove serena from git tracking?

## Marketing

[ ] Make PRs in popular list repos like <https://github.com/hesreallyhim/awesome-claude-code>
