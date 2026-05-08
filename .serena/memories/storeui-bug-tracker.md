# Storeui bug tracker

- [ ] multiline boxes should accept shift+enter for new lines and enter to save 
- [ ] Audit fields for usage — "build.timeout", "build.start_period", "build.retries", "agent.includes" may be unused/legacy
- [ ] "agent.memory" and similar fields should be grouped in an "advanced" collapsible section
- [ ] Field descriptions inaccurate — "command" says "healthchecks", SHELL says "Default shell for RUN instructions" but it's the terminal shell env var
- [ ] firewall rules editor should be a structured form, not multiline text
- [ ] firewall rules preview shows raw Go map literal instead of formatted display
- [ ] firewall rules duplication bug (github ssh appears twice)
