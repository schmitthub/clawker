package tui

// HookResult controls execution flow after a lifecycle hook fires.
type HookResult struct {
	Continue bool   // false = quit execution
	Message  string // reason for quitting (only meaningful when Continue=false)
	Err      error  // hook's own failure (independent of Continue)
}

// LifecycleHook is called at key moments during TUI component execution.
// component identifies the source (e.g., "progress"), event names the moment
// (e.g., "before_complete"). Implementations may block (for pausing) or return
// quickly (for logging). nil hooks are never called — components check before firing.
type LifecycleHook func(component, event string) HookResult
