# TODO.md

## Bugs

[ ] claucker start is creating new containers instead of attaching to existing
[ ] claucker list is not showing all the stopped agent containers
[ ] Serena's language server isn't initialized. Let me use standard tools to explore the engine code.
[ ] go not found in path, has to find it /usr/local/go/bin/go
[ ] Terminal still locks when quitting out of the container when the CC initial setup page (auth, accept dir, etc) is active
[ ] we need to copy project claude settings local files and not bind so that command approvals aren't shared

## Enhancements

[ ] see if there is a way to pass browser opening events to the host and then back into the container. for example for claude auth, and for mcp's like serena
[ ] add a statusline to every container so the user knows which container they are in along with kewl metrics. Update Statusline monitor to calculate session limit, maybe consider using python
[ ] really need to think through the verbage for the cli to run them and what they do and make sure everything is either cleaned up or accessed instead of making new containers over and over again. Should add an `exec` command to run something in an existing instance
[ ] start --detach should not use `claude` as entrypoint
[ ] can you rename @pkg/build/ to something like pkg/package/ since it is simply creating dockerfiles  based on current claude code changes. could there be a better name to fit trends to fit this behavior better?
[ ] Claude file mounting from host strategy. Balancing convenience with sharing with host tradeoffs. So maybe two claude modes (shared (bind) vs fresh(do nothing) vs isolated (copy)) vs two workspace modes (shared (bind), vs isolated (copy))
[ ] ZSH install and oh my zsh should be an optional. Come to think of it have the recommended image you like to use and make all of those things optional so that most ppl can just hit the ground running
[ ] Add "include language" build options to make adding build tools for each languange easy
[ ] Add man pages like gh. Describe config file in detail etc
[ ] Config properties are confusing (ie "instructions" but actually is pure shell commands) Injection poitns should be all we use that take in proper docker build instructions
[ ] Go docs
[ ] github pages site w/ hugo mkdocs
[ ] container tags need use the container naming convention
[ ] Claucker-generate is probably dumb and should just be a standalone go script
[ ] Add a light monitoring shell UI that is aware of claude subs vs claude sdk api costs
[ ] Multi image support. you can create as many or as little claucker containers as you want
[ ] Add progress bars with status updates to CLI output instead of verbose logs endless log entries in the terminal
[ ] Add session tracking by container name in metrics collection, explained in metrics docs
[ ] Make an SRE/Devops specialist sub-agent to troubelshoot container and service issues because it destroys the context
  window
[ ] Go cli agent needs work. needs to use opus, needs to have a refactor skill / feature updater
[ ] Fix generate cmd output its too verbose
[ ] claucker ls should have an alias to list. -a should show monitoring containers (anything on claucker-net)
[ ] claucker output should mirror claude code ANSI style
[ ] MCP strategy but serena seems to be working inside the container
