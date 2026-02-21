package project

import "github.com/schmitthub/clawker/internal/config"

type ProjectHandleForTest = projectHandle

func NewProjectHandleForTest(record ProjectRecord) *projectHandle {
	return &projectHandle{record: record}
}

type RegistryForTest struct {
	registry *projectRegistry
}

func NewRegistryForTest(cfg config.Config) *RegistryForTest {
	return &RegistryForTest{registry: newRegistry(cfg)}
}

func (r *RegistryForTest) RemoveByRoot(root string) error {
	return r.registry.RemoveByRoot(root)
}

func (r *RegistryForTest) Projects() []config.ProjectEntry {
	return r.registry.Projects()
}

func (r *RegistryForTest) Update(entry config.ProjectEntry) (config.ProjectEntry, error) {
	return r.registry.Update(entry)
}

func (r *RegistryForTest) Save() error {
	return r.registry.Save()
}

func (r *RegistryForTest) ProjectByRoot(root string) (config.ProjectEntry, bool, error) {
	return r.registry.ProjectByRoot(root)
}
