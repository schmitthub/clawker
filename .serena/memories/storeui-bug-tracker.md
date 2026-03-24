# Storeui bug tracker

- [x] map[string]string fields like "agent.env" just use a multi line text editor... 
- [ ] imo multiline boxes should accept shift+enter for new lines and enter to save 
- [x] trying to save instructions shows a "project" option when there the project level file is already in the options. resultin in a bug — FIXED: virtual layers (empty path) were not filtered from LayerTargets; also fixed same bug in settings adapter; label changed from "Project"/"Settings" to "Original". LayerTargets logic extracted to shared `storeui.BuildLayerTargets()`.
- [x] after saving a field to a non-winning layer, the UI shows the saved value instead of the true merged value — FIXED: added `storage.Store.Refresh()` (re-reads layers from disk + re-merges + republishes snapshot); storeui calls it after every Set+Write and Delete+Write
- [ ] Lots of fields seem legacy like "build.timeout", "build.start_period", "build.retries", "agent.includes". Are these still used? Can we remove them? We need to audit all fields for usage and remove any that are no longer used.
- [ ] "agent.memory", and several other fields, are for advanced users and justtifying a label field of advanced to group them in a collapsable section or the like  
- [ ] Descriptions seem off like for command. says its for "healchecks"... is it really just the container command lol. SHELL is an env var for the default shell for terminal sessions... not specifically "Default shell for RUN instructions"
- [x] i'm leaning towards removing "alpine" vs "debian" variants for commands it kind of doesn't make any sense to me 
- [x] instructions.root_run|user_run need to be multiline. all text fields need to be multiline as per yaml spec 
